package kubesphere

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cookieSecret marks every credential-like value written to the fixture files,
// so a leak into an error message is detectable.
const cookieSecret = "SYNTHETIC-SECRET"

func originOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u.Scheme + "://" + u.Host
}

// writeSessionJSON replaces the session file atomically, like the writer does.
func writeSessionJSON(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	tmp := path + ".tmp"
	require.NoError(t, os.WriteFile(tmp, []byte(content), mode))
	require.NoError(t, os.Chmod(tmp, mode))
	require.NoError(t, os.Rename(tmp, path))
}

func sessionJSON(origin, cookie string, expires time.Time) string {
	return fmt.Sprintf(`{"sessions":{%q:{"value":%q,"expires":%d,"updatedAt":1,"capturedAt":1}}}`, origin, cookie, expires.Unix())
}

func cookieClient(t *testing.T, serverURL, path string, argv []string) *http.Client {
	t.Helper()
	transport, err := newSessionCookieTransport(http.DefaultTransport, path, serverURL, argv)
	require.NoError(t, err)
	return &http.Client{Transport: transport}
}

func TestCookieFileSendsCurrentSession(t *testing.T) {
	var got atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Values("Cookie"))
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "session.json")
	origin := originOf(t, server.URL)
	writeSessionJSON(t, path, sessionJSON(origin, "token=one", time.Now().Add(time.Hour)), 0o600)
	client := cookieClient(t, server.URL, path, nil)

	req, err := http.NewRequest(http.MethodGet, server.URL+"/kapis/x", nil)
	require.NoError(t, err)
	req.Header.Set("Cookie", "caller=1")
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, []string{"token=one"}, got.Load(), "file cookie replaces the caller's Cookie header")
	assert.Equal(t, []string{"caller=1"}, req.Header.Values("Cookie"), "caller's request is not mutated")

	writeSessionJSON(t, path, sessionJSON(origin, "token=two", time.Now().Add(time.Hour)), 0o600)
	resp, err = client.Get(server.URL + "/kapis/x")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, []string{"token=two"}, got.Load(), "rotated file is used by the same client")
}

func TestCookieFileRejectsUnusableSession(t *testing.T) {
	good := func(origin string) string {
		return sessionJSON(origin, "token="+cookieSecret, time.Now().Add(time.Hour))
	}
	for _, tc := range []struct {
		name    string
		content func(origin string) string
		mode    os.FileMode
		want    string
	}{
		{"missing file", nil, 0o600, "no such file"},
		{"malformed JSON", func(string) string { return `{"sessions": ` + cookieSecret }, 0o600, "not a valid origin-keyed session file"},
		{"trailing data", func(o string) string { return good(o) + cookieSecret }, 0o600, "not a valid origin-keyed session file"},
		{"unknown field", func(o string) string {
			return strings.Replace(good(o), `"updatedAt"`, `"`+cookieSecret+`":1,"updatedAt"`, 1)
		}, 0o600, "not a valid origin-keyed session file"},
		{"other origin only", func(string) string { return good("https://other.example.com") }, 0o600, "has no session for"},
		{"expired", func(o string) string { return sessionJSON(o, "token="+cookieSecret, time.Now().Add(-time.Minute)) }, 0o600, "expired"},
		{"inside expiry grace", func(o string) string { return sessionJSON(o, "token="+cookieSecret, time.Now().Add(30*time.Second)) }, 0o600, "expired"},
		{"empty cookie header", func(o string) string { return sessionJSON(o, "", time.Now().Add(time.Hour)) }, 0o600, "not a valid Cookie header"},
		{"invalid cookie header", func(o string) string {
			return sessionJSON(o, "token="+cookieSecret+"\r\nX-Injected: 1", time.Now().Add(time.Hour))
		}, 0o600, "not a valid Cookie header"},
		{"group readable", good, 0o640, "must not be accessible by group or others"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mode&0o077 != 0 && runtime.GOOS == "windows" {
				t.Skip("Windows has no POSIX permission bits")
			}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
			defer server.Close()

			path := filepath.Join(t.TempDir(), "session.json")
			if tc.content != nil {
				writeSessionJSON(t, path, tc.content(originOf(t, server.URL)), tc.mode)
			}
			resp, err := cookieClient(t, server.URL, path, nil).Get(server.URL + "/kapis/x")
			if resp != nil {
				_ = resp.Body.Close()
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), cookieSecret)
			assert.Zero(t, requests.Load(), "no request may reach KubeSphere without a valid session")
		})
	}

	t.Run("relative path", func(t *testing.T) {
		_, err := newSessionCookieTransport(http.DefaultTransport, "session.json", "https://ks.example.com", nil)
		require.ErrorContains(t, err, "absolute path")
	})
}

func TestCookieFileOriginBinding(t *testing.T) {
	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignRequests.Add(1) }))
	defer foreign.Close()

	var ks *httptest.Server
	ks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "token=ks", r.Header.Get("Cookie"))
		switch r.URL.Path {
		case "/same-origin":
			http.Redirect(w, r, ks.URL+"/landed", http.StatusFound)
		case "/cross-origin":
			http.Redirect(w, r, foreign.URL+"/capture", http.StatusFound)
		}
	}))
	defer ks.Close()

	// The shared file holds a session for the foreign origin as well, so the
	// transport could leak it if it selected sessions by request origin.
	path := filepath.Join(t.TempDir(), "session.json")
	exp := time.Now().Add(time.Hour).Unix()
	writeSessionJSON(t, path, fmt.Sprintf(
		`{"sessions":{%q:{"value":"token=ks","expires":%d,"updatedAt":1},%q:{"value":"token=foreign","expires":%d,"updatedAt":1}}}`,
		originOf(t, ks.URL), exp, originOf(t, foreign.URL), exp), 0o600)
	client := cookieClient(t, ks.URL, path, nil)

	resp, err := client.Get(foreign.URL + "/direct")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.ErrorContains(t, err, "refusing to send session")

	resp, err = client.Get(ks.URL + "/cross-origin")
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.Zero(t, foreignRequests.Load(), "a cross-origin redirect never carries the session")

	resp, err = client.Get(ks.URL + "/same-origin")
	require.NoError(t, err)
	_ = resp.Body.Close()
}
