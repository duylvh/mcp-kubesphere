package kubesphere

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKAPIPath(t *testing.T) {
	for _, tc := range []struct {
		cluster, gv string
		segments    []string
		want        string
	}{
		{"", "resources.kubesphere.io/v1alpha3", []string{"clusters"}, "/kapis/resources.kubesphere.io/v1alpha3/clusters"},
		{"dev", "resources.kubesphere.io/v1alpha3", []string{"namespaces", "team-project", "pods"}, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/namespaces/team-project/pods"},
		{"", "iam.kubesphere.io/v1alpha2", []string{"users", "a@b.c"}, "/kapis/iam.kubesphere.io/v1alpha2/users/a@b.c"},
		{"", "iam.kubesphere.io/v1alpha2", []string{"users", "a b"}, "/kapis/iam.kubesphere.io/v1alpha2/users/a%20b"},
	} {
		got, err := KAPIPath(tc.cluster, tc.gv, tc.segments...)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "a?x=1", "a#f", "%2e%2e", "a\nb"} {
		_, err := KAPIPath("", "iam.kubesphere.io/v1alpha2", "users", bad)
		assert.Error(t, err, "segment %q", bad)
		_, err = KAPIPath(bad, "iam.kubesphere.io/v1alpha2", "users")
		if bad != "" {
			assert.Error(t, err, "cluster %q", bad)
		}
	}
}

func cookieKSConfig(t *testing.T, serverURL string, argv []string) (*KSConfig, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.json")
	writeSessionJSON(t, path, sessionJSON(originOf(t, serverURL), "token=old", time.Now().Add(time.Hour)), 0o600)
	ks, err := NewKSConfig(Options{APIServer: serverURL, CookieFile: path, CookieRefreshCommand: argv})
	require.NoError(t, err)
	return ks, path
}

func TestGetReturnsJSONAndKubeSphereErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/kapis/ok":
			assert.Equal(t, "limit=10&page=1", r.URL.RawQuery)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[]}`))
		case "/kapis/forbidden":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"kind":"Status","message":"pods is forbidden: User \"u\" cannot list resource"}`))
		}
	}))
	defer server.Close()
	ks, _ := cookieKSConfig(t, server.URL, nil)

	body, err := ks.Get(t.Context(), "/kapis/ok", map[string][]string{"limit": {"10"}, "page": {"1"}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"items":[]}`, string(body))

	_, err = ks.Get(t.Context(), "/kapis/forbidden", nil)
	require.ErrorContains(t, err, `KubeSphere returned 403 for /kapis/forbidden: pods is forbidden: User "u" cannot list resource`)
}

// The console answers a path it does not proxy with its HTML page. That is a
// wrong path, not an expired session, so it must not trigger a sign-in.
func TestGetHTMLIsAnErrorWithoutRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!DOCTYPE html><html>" + cookieSecret + "</html>"))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "session.json")
	fake := newFakeRefresh(t, path, originOf(t, server.URL), "token=new", false, "0")
	ks, _ := cookieKSConfig(t, server.URL, fake.argv)

	_, err := ks.Get(t.Context(), "/clusters/dev/kapis/x", nil)
	require.ErrorContains(t, err, "HTML page instead of JSON for /clusters/dev/kapis/x")
	assert.NotContains(t, err.Error(), cookieSecret, "the page body is never quoted")
	assert.Zero(t, fake.runs(t))
}

func TestGetDoesNotFollowRedirects(t *testing.T) {
	var landed int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			landed++
			return
		}
		http.Redirect(w, r, "/login?referer="+r.URL.Path, http.StatusFound)
	}))
	defer server.Close()
	ks, _ := cookieKSConfig(t, server.URL, nil)

	_, err := ks.Get(t.Context(), "/kapis/x", nil)
	require.ErrorContains(t, err, "redirected /kapis/x to "+server.URL+"/login; the session is not signed in")
	assert.Zero(t, landed, "the login page is never fetched as data")
}

func TestNewKSConfigModes(t *testing.T) {
	_, err := NewKSConfig(Options{CookieFile: "/tmp/session.json"})
	require.ErrorContains(t, err, URLEnvVar)

	_, err = NewKSConfig(Options{})
	require.ErrorContains(t, err, "--ksconfig")

	_, err = NewKSConfig(Options{APIServer: "https://ks.example.com", CookieRefreshCommand: []string{"refresh"}})
	require.ErrorContains(t, err, "requires "+CookieFileEnvVar)

	dir := t.TempDir()
	noToken := filepath.Join(dir, "password.yaml")
	require.NoError(t, os.WriteFile(noToken, []byte(`apiVersion: v1
kind: Config
clusters: [{name: kubesphere, cluster: {server: "https://ks.example.com"}}]
users: [{name: admin, user: {username: admin, password: secret}}]
contexts: [{name: kubesphere, context: {cluster: kubesphere, user: admin}}]
current-context: kubesphere
`), 0o600))
	_, err = NewKSConfig(Options{KSConfig: noToken})
	require.ErrorContains(t, err, "bearer token", "the password grant is gone; a token is required")

	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	withToken := filepath.Join(dir, "token.yaml")
	require.NoError(t, os.WriteFile(withToken, []byte(`apiVersion: v1
kind: Config
clusters: [{name: kubesphere, cluster: {server: "https://unused.example.com"}}]
users: [{name: me, user: {token: synthetic-token}}]
contexts: [{name: kubesphere, context: {cluster: kubesphere, user: me}}]
current-context: kubesphere
`), 0o600))
	ks, err := NewKSConfig(Options{KSConfig: withToken, APIServer: server.URL})
	require.NoError(t, err)
	_, err = ks.Get(t.Context(), "/kapis/x", nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer synthetic-token", gotAuth)
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv(URLEnvVar, "https://ks.example.com")
	t.Setenv(CookieFileEnvVar, "/run/session.json")
	t.Setenv(CookieRefreshCommandEnvVar, `["refresh","kubesphere"]`)
	o, err := OptionsFromEnv("", "https://flag.example.com")
	require.NoError(t, err)
	assert.Equal(t, Options{APIServer: "https://ks.example.com", CookieFile: "/run/session.json", CookieRefreshCommand: []string{"refresh", "kubesphere"}}, o)

	t.Setenv(CookieRefreshCommandEnvVar, "not json")
	_, err = OptionsFromEnv("", "")
	require.Error(t, err)

	t.Setenv(CookieFileEnvVar, "")
	t.Setenv(CookieRefreshCommandEnvVar, `["refresh"]`)
	_, err = OptionsFromEnv("", "")
	require.ErrorContains(t, err, "requires "+CookieFileEnvVar)
}

func TestGetTooLargeAdvisesSmallerLimitOnlyWhenPaging(t *testing.T) {
	saved := maxResponseSize
	maxResponseSize = 16
	t.Cleanup(func() { maxResponseSize = saved })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[1,2,3,4,5,6,7,8,9]}`))
	}))
	t.Cleanup(server.Close)
	ks, _ := cookieKSConfig(t, server.URL, nil)

	for _, tc := range []struct {
		query url.Values
		hint  bool
	}{
		{url.Values{"limit": {"10"}, "page": {"1"}}, true},
		{url.Values{"paging": {"limit=10,page=1"}}, true},
		{url.Values{"fieldSelector": {"involvedObject.name=svc-0"}}, false},
		{nil, false},
	} {
		_, err := ks.Get(t.Context(), "/api/clusters/dev/v1/namespaces/p1/events", tc.query)
		require.Error(t, err, "%v", tc.query)
		assert.Contains(t, err.Error(), "exceeds")
		if tc.hint {
			assert.Contains(t, err.Error(), "use a smaller limit", "%v", tc.query)
		} else {
			assert.NotContains(t, err.Error(), "limit", "%v", tc.query)
		}
	}
}
