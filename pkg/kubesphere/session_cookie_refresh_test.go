package kubesphere

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRefresh is a refresh command that records each run and, unless it is
// set to fail, writes a valid session holding newCookie.
type fakeRefresh struct {
	argv  []string
	count string
}

func newFakeRefresh(t *testing.T, sessionPath, origin, newCookie string, fail bool, delay string) *fakeRefresh {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake refresh command is a POSIX shell script")
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "runs")
	script := filepath.Join(dir, "refresh.sh")
	body := fmt.Sprintf("#!/bin/sh\necho run >> %q\nsleep %s\n", count, delay)
	if fail {
		body += "exit 1\n"
	} else {
		body += fmt.Sprintf("printf '%%s' '%s' > %q.tmp && chmod 600 %q.tmp && mv %q.tmp %q\n",
			sessionJSON(origin, newCookie, time.Now().Add(time.Hour)), sessionPath, sessionPath, sessionPath, sessionPath)
	}
	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))
	return &fakeRefresh{argv: []string{script}, count: count}
}

func (f *fakeRefresh) runs(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(f.count)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	return strings.Count(string(data), "run")
}

// ksFake accepts only the cookie "token=new" and records what it received.
type ksFake struct {
	*httptest.Server
	mu      sync.Mutex
	cookies []string
}

func newKSFake(t *testing.T, onStale http.HandlerFunc) *ksFake {
	t.Helper()
	k := &ksFake{}
	k.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie := r.Header.Get("Cookie")
		k.mu.Lock()
		k.cookies = append(k.cookies, cookie)
		k.mu.Unlock()
		if cookie != "token=new" {
			onStale(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"totalItems":0}`))
	}))
	t.Cleanup(k.Close)
	return k
}

func (k *ksFake) seen() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.cookies...)
}

func unauthorized(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }

func status(t *testing.T, resp *http.Response, err error) int {
	t.Helper()
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestCookieRefreshMissingSession(t *testing.T) {
	k := newKSFake(t, unauthorized)
	path := filepath.Join(t.TempDir(), "session.json")
	fake := newFakeRefresh(t, path, originOf(t, k.URL), "token=new", false, "0")

	resp, err := cookieClient(t, k.URL, path, fake.argv).Get(k.URL + "/kapis/x")
	assert.Equal(t, http.StatusOK, status(t, resp, err))
	assert.Equal(t, []string{"token=new"}, k.seen(), "the stale session is never sent and the new one is used once")
	assert.Equal(t, 1, fake.runs(t))
}

func TestCookieRefreshAfterRejectedSession(t *testing.T) {
	for name, onStale := range map[string]http.HandlerFunc{
		"401": unauthorized,
		"redirect to console login": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/login?referer=/kapis/x", http.StatusFound)
		},
		"redirect to SSO server": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://cas.example.com/sso/login", http.StatusFound)
		},
	} {
		t.Run(name, func(t *testing.T) {
			k := newKSFake(t, onStale)
			path := filepath.Join(t.TempDir(), "session.json")
			origin := originOf(t, k.URL)
			writeSessionJSON(t, path, sessionJSON(origin, "token=old", time.Now().Add(time.Hour)), 0o600)
			fake := newFakeRefresh(t, path, origin, "token=new", false, "0")

			resp, err := cookieClient(t, k.URL, path, fake.argv).Get(k.URL + "/kapis/x")
			assert.Equal(t, http.StatusOK, status(t, resp, err))
			assert.Equal(t, []string{"token=old", "token=new"}, k.seen())
			assert.Equal(t, 1, fake.runs(t))
		})
	}
}

func TestCookieRefreshNotTriggeredByRBACOrSameOriginRedirect(t *testing.T) {
	var k *ksFake
	k = newKSFake(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/forbidden":
			w.WriteHeader(http.StatusForbidden)
		case "/moved":
			http.Redirect(w, r, k.URL+"/landed", http.StatusFound)
		}
	})
	path := filepath.Join(t.TempDir(), "session.json")
	origin := originOf(t, k.URL)
	writeSessionJSON(t, path, sessionJSON(origin, "token=old", time.Now().Add(time.Hour)), 0o600)
	fake := newFakeRefresh(t, path, origin, "token=new", false, "0")
	client := cookieClient(t, k.URL, path, fake.argv)

	resp, err := client.Get(k.URL + "/forbidden")
	assert.Equal(t, http.StatusForbidden, status(t, resp, err))
	resp, err = client.Get(k.URL + "/moved")
	assert.Equal(t, http.StatusOK, status(t, resp, err))
	assert.Zero(t, fake.runs(t), "403 is RBAC and a same-origin redirect is not a sign-in page")
}

func TestCookieRefreshUsesSessionSavedByAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	var origin string
	k := newKSFake(t, func(w http.ResponseWriter, _ *http.Request) {
		writeSessionJSON(t, path, sessionJSON(origin, "token=new", time.Now().Add(time.Hour)), 0o600)
		w.WriteHeader(http.StatusUnauthorized)
	})
	origin = originOf(t, k.URL)
	writeSessionJSON(t, path, sessionJSON(origin, "token=old", time.Now().Add(time.Hour)), 0o600)
	fake := newFakeRefresh(t, path, origin, "token=new", false, "0")

	resp, err := cookieClient(t, k.URL, path, fake.argv).Get(k.URL + "/kapis/x")
	assert.Equal(t, http.StatusOK, status(t, resp, err))
	assert.Equal(t, []string{"token=old", "token=new"}, k.seen())
	assert.Equal(t, 0, fake.runs(t), "a session already renewed in the file needs no sign-in")
}

func TestCookieRefreshFailureBacksOff(t *testing.T) {
	k := newKSFake(t, unauthorized)
	path := filepath.Join(t.TempDir(), "session.json")
	fake := newFakeRefresh(t, path, originOf(t, k.URL), "token="+cookieSecret, true, "0")

	transport, err := newSessionCookieTransport(http.DefaultTransport, path, k.URL, fake.argv)
	require.NoError(t, err)
	now := time.Now()
	transport.refresher.now = func() time.Time { return now }
	client := &http.Client{Transport: transport}

	get := func() error {
		resp, err := client.Get(k.URL + "/kapis/x")
		if resp != nil {
			_ = resp.Body.Close()
		}
		return err
	}
	for range 2 {
		err := get()
		require.ErrorContains(t, err, errCookieRefreshFailed)
		assert.NotContains(t, err.Error(), cookieSecret)
	}
	assert.Equal(t, 1, fake.runs(t), "the second request inside the back-off window does not run the command")

	now = now.Add(cookieRefreshBackoff + time.Second)
	require.Error(t, get())
	assert.Equal(t, 2, fake.runs(t), "the command runs again once the back-off has passed")
	assert.Empty(t, k.seen(), "no request reaches KubeSphere without a session")
}

func TestCookieRefreshIsSingleFlight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial string
	}{
		{"missing session", ""},
		{"rejected session", "token=old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newKSFake(t, unauthorized)
			path := filepath.Join(t.TempDir(), "session.json")
			origin := originOf(t, k.URL)
			if tc.initial != "" {
				writeSessionJSON(t, path, sessionJSON(origin, tc.initial, time.Now().Add(time.Hour)), 0o600)
			}
			fake := newFakeRefresh(t, path, origin, "token=new", false, "0.3")
			client := cookieClient(t, k.URL, path, fake.argv)

			const n = 8
			codes := make([]int, n)
			var wg sync.WaitGroup
			for i := range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					resp, err := client.Get(k.URL + "/kapis/x")
					if err == nil {
						_ = resp.Body.Close()
						codes[i] = resp.StatusCode
					}
				}()
			}
			wg.Wait()

			for _, code := range codes {
				assert.Equal(t, http.StatusOK, code)
			}
			assert.Equal(t, 1, fake.runs(t))
		})
	}
}

func TestCookieRefreshWaiterHonorsContext(t *testing.T) {
	k := newKSFake(t, unauthorized)
	path := filepath.Join(t.TempDir(), "session.json")
	fake := newFakeRefresh(t, path, originOf(t, k.URL), "token=new", false, "1")
	client := cookieClient(t, k.URL, path, fake.argv)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.URL+"/kapis/x", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestParseCookieRefreshCommand(t *testing.T) {
	argv, err := ParseCookieRefreshCommand(`["bash","/abs/zlpwork","session","refresh","kubesphere"]`)
	require.NoError(t, err)
	assert.Equal(t, []string{"bash", "/abs/zlpwork", "session", "refresh", "kubesphere"}, argv)

	for _, raw := range []string{`bash /abs/zlpwork`, `["bash",`, `[]`, `[""]`, `"bash"`, `[1]`} {
		_, err := ParseCookieRefreshCommand(raw)
		require.Error(t, err, raw)
		assert.NotContains(t, err.Error(), "zlpwork", "the error does not echo the value")
	}
}

func TestCookieRefreshSharedPerSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	a, err := newSessionCookieTransport(http.DefaultTransport, path, "https://ks.example.com", []string{"refresh"})
	require.NoError(t, err)
	b, err := newSessionCookieTransport(http.DefaultTransport, path, "https://ks.example.com", []string{"refresh"})
	require.NoError(t, err)
	other, err := newSessionCookieTransport(http.DefaultTransport, path, "https://other.example.com", []string{"refresh"})
	require.NoError(t, err)
	assert.Same(t, a.refresher, b.refresher, "every transport of a session shares one refresher")
	assert.NotSame(t, a.refresher, other.refresher)
}

func TestSessionCookieFileRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevated rights on Windows")
	}
	k := newKSFake(t, unauthorized)
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	writeSessionJSON(t, real, sessionJSON(originOf(t, k.URL), "token=new", time.Now().Add(time.Hour)), 0o600)
	link := filepath.Join(dir, "session.json")
	require.NoError(t, os.Symlink(real, link))
	fake := newFakeRefresh(t, link, originOf(t, k.URL), "token=new", false, "0")

	for name, argv := range map[string][]string{"plain": nil, "with refresh": fake.argv} {
		t.Run(name, func(t *testing.T) {
			resp, err := cookieClient(t, k.URL, link, argv).Get(k.URL + "/kapis/x")
			if resp != nil {
				_ = resp.Body.Close()
			}
			require.ErrorContains(t, err, "must not be a symlink")
		})
	}
	assert.Empty(t, k.seen())
	assert.Zero(t, fake.runs(t), "a symlink needs the user, not a sign-in")
}

func TestCookieRefreshNeverSeesForeignRedirectTarget(t *testing.T) {
	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { foreignRequests.Add(1) }))
	defer foreign.Close()
	k := newKSFake(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+"/login", http.StatusFound)
	})
	path := filepath.Join(t.TempDir(), "session.json")
	origin := originOf(t, k.URL)
	writeSessionJSON(t, path, sessionJSON(origin, "token=old", time.Now().Add(time.Hour)), 0o600)
	fake := newFakeRefresh(t, path, origin, "token=new", false, "0")

	resp, err := cookieClient(t, k.URL, path, fake.argv).Get(k.URL + "/kapis/x")
	assert.Equal(t, http.StatusOK, status(t, resp, err))
	assert.Equal(t, 1, fake.runs(t))
	assert.Zero(t, foreignRequests.Load(), "the sign-in redirect is replaced by the retry, never followed")
}

// A refresh slower than the request timeout must not cancel the request
// waiting for it: the timeout bounds each hop, not the whole call.
func TestSlowRefreshOutlastsRequestTimeout(t *testing.T) {
	saved := requestTimeout
	requestTimeout = 200 * time.Millisecond
	t.Cleanup(func() { requestTimeout = saved })

	k := newKSFake(t, unauthorized)
	path := filepath.Join(t.TempDir(), "session.json")
	fake := newFakeRefresh(t, path, originOf(t, k.URL), "token=new", false, "0.6")
	ks, err := NewKSConfig(Options{APIServer: k.URL, CookieFile: path, CookieRefreshCommand: fake.argv})
	require.NoError(t, err)

	body, err := ks.Get(t.Context(), "/kapis/x", nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"items":[],"totalItems":0}`, string(body))
	assert.Equal(t, 1, fake.runs(t))
}
