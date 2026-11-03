package kubesphere

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// sessionExpiryGrace treats a cookie that expires sooner as already expired,
	// so a request never leaves with a session that dies on the way.
	sessionExpiryGrace = 60 * time.Second
	maxSessionFileSize = 1 << 20
)

// sessionFile is the origin-keyed session file shared with the zlpwork and
// mcp-grafana tools. Origins use the browser URL.origin form, e.g.
// "https://ks.example.com".
type sessionFile struct {
	Sessions map[string]sessionCookie `json:"sessions"`
}

type sessionCookie struct {
	// Value is a complete Cookie request header value, e.g. "token=abc".
	Value string `json:"value"`
	// Expires, UpdatedAt and CapturedAt are Unix seconds.
	Expires    int64 `json:"expires"`
	UpdatedAt  int64 `json:"updatedAt"`
	CapturedAt int64 `json:"capturedAt,omitzero"`
}

// sessionCookieTransport replaces the Cookie header of every request with the
// session stored in a file for the configured KubeSphere origin. The file is
// read on each request so a rotated session is used without restarting.
// Errors never contain file contents, and a request is never sent without the
// cookie.
type sessionCookieTransport struct {
	next    http.RoundTripper
	path    string
	baseURL *url.URL
	origin  string
	// refresher is nil unless a refresh command is configured.
	refresher *cookieRefresher
}

// staleSessionError marks a missing or expired session, the cases a refresh
// command can repair, apart from a malformed file or a refused origin.
type staleSessionError struct{ err error }

func (e *staleSessionError) Error() string { return e.err.Error() }
func (e *staleSessionError) Unwrap() error { return e.err }

func newSessionCookieTransport(next http.RoundTripper, path, baseURL string, refreshCommand []string) (*sessionCookieTransport, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("kubesphere cookie file must be an absolute path")
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, errors.New("kubesphere cookie file requires a valid KubeSphere URL")
	}
	origin, ok := sessionOrigin(u)
	if !ok {
		return nil, errors.New("kubesphere cookie file requires an http(s) KubeSphere URL")
	}
	t := &sessionCookieTransport{next: next, path: path, baseURL: u, origin: origin}
	if len(refreshCommand) > 0 {
		t.refresher = cookieRefresherFor(path, origin, refreshCommand)
	}
	return t, nil
}

// sessionOrigin formats u like the browser's URL.origin, which is how the
// session writer keys the file.
func sessionOrigin(u *url.URL) (string, bool) {
	scheme, host, port, ok := httpOriginParts(u)
	if !ok {
		return "", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme + "://" + host, true
	}
	return scheme + "://" + host + ":" + port, true
}

// RoundTrip sends req with the session cookie. With a refresh command, a
// missing or expired session, a 401, or a redirect to a sign-in page renews
// the session and retries once.
func (t *sessionCookieTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.refresher == nil {
		resp, _, err := t.send(req)
		return resp, err
	}
	generation := t.refresher.Generation()
	resp, sent, err := t.send(req)
	if !t.sessionRejected(req, resp, err) || !replayable(req) {
		return resp, err
	}
	if resp != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
	}
	// Another process may have signed in since this request read the file;
	// its session is used before paying for a new sign-in.
	if stored, err := t.cookieFor(req.URL); err != nil || stored == sent {
		if err := t.refresher.Refresh(req.Context(), generation); err != nil {
			return nil, err
		}
	}
	retry, err := rewound(req)
	if err != nil {
		return nil, err
	}
	resp, _, err = t.send(retry)
	return resp, err
}

// send returns the cookie it sent, empty when the request was not sent.
func (t *sessionCookieTransport) send(req *http.Request) (*http.Response, string, error) {
	cookie, err := t.cookieFor(req.URL)
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, "", err
	}
	cloned := req.Clone(req.Context())
	cloned.Header.Set("Cookie", cookie)
	resp, err := t.next.RoundTrip(cloned)
	return resp, cookie, err
}

// sessionRejected reports the responses a new sign-in can fix: a 401, or a
// redirect to the console's /login page or to another origin (the SSO server).
// An HTML page served with 200 is not one of them: it means the request path
// is not an API route, and signing in again would not change that.
func (t *sessionCookieTransport) sessionRejected(req *http.Request, resp *http.Response, err error) bool {
	if err != nil {
		var stale *staleSessionError
		return errors.As(err, &stale)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return false
	}
	location, err := req.URL.Parse(resp.Header.Get("Location"))
	if err != nil {
		return false
	}
	return !sameHTTPOrigin(location, t.baseURL) || location.Path == "/login" || strings.HasPrefix(location.Path, "/login/")
}

func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// rewound returns req with a fresh body, since sending consumed the original.
func rewound(req *http.Request) (*http.Request, error) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	retry := req.Clone(req.Context())
	retry.Body = body
	return retry, nil
}

func (t *sessionCookieTransport) cookieFor(target *url.URL) (string, error) {
	if !sameHTTPOrigin(target, t.baseURL) {
		return "", fmt.Errorf("kubesphere cookie file: refusing to send session for %s to %s", t.origin, redirectOriginForError(target))
	}
	session, err := t.readSession()
	if err != nil {
		return "", err
	}
	if session.Expires <= time.Now().Add(sessionExpiryGrace).Unix() {
		return "", &staleSessionError{fmt.Errorf("kubesphere cookie file: session for %s is expired or expires within %s; sign in to KubeSphere in the browser again", t.origin, sessionExpiryGrace)}
	}
	if _, err := http.ParseCookie(session.Value); err != nil {
		return "", fmt.Errorf("kubesphere cookie file: session for %s is not a valid Cookie header value", t.origin)
	}
	return session.Value, nil
}

// readSession refuses a symlinked file, so the session cannot be redirected to
// another location.
func (t *sessionCookieTransport) readSession() (sessionCookie, error) {
	f, err := os.OpenFile(t.path, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		if isSymlinkOpenError(err) {
			return sessionCookie{}, fmt.Errorf("kubesphere cookie file: %s must not be a symlink", t.path)
		}
		err = fmt.Errorf("kubesphere cookie file: %w", err)
		if errors.Is(err, fs.ErrNotExist) {
			err = &staleSessionError{err}
		}
		return sessionCookie{}, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return sessionCookie{}, fmt.Errorf("kubesphere cookie file: %s is not a readable regular file", t.path)
	}
	// Windows has no POSIX permission bits, so only enforce this elsewhere.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return sessionCookie{}, fmt.Errorf("kubesphere cookie file: %s must not be accessible by group or others (chmod 600)", t.path)
	}

	// Decode errors are replaced with a fixed message because they can quote file content.
	dec := json.NewDecoder(io.LimitReader(f, maxSessionFileSize))
	dec.DisallowUnknownFields()
	var file sessionFile
	if dec.Decode(&file) != nil || dec.Decode(&struct{}{}) != io.EOF {
		return sessionCookie{}, fmt.Errorf("kubesphere cookie file: %s is not a valid origin-keyed session file", t.path)
	}
	session, ok := file.Sessions[t.origin]
	if !ok {
		return sessionCookie{}, &staleSessionError{fmt.Errorf("kubesphere cookie file: %s has no session for %s", t.path, t.origin)}
	}
	return session, nil
}

func redirectOriginForError(u *url.URL) string {
	if u == nil {
		return "<unknown>"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

func sameHTTPOrigin(a, b *url.URL) bool {
	aScheme, aHost, aPort, aOK := httpOriginParts(a)
	bScheme, bHost, bPort, bOK := httpOriginParts(b)
	return aOK && bOK && aScheme == bScheme && aHost == bHost && aPort == bPort
}

func httpOriginParts(u *url.URL) (scheme, host, port string, ok bool) {
	if u == nil {
		return "", "", "", false
	}
	scheme = strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" || u.Hostname() == "" {
		return "", "", "", false
	}
	rawHost := u.Hostname()
	if addr, err := netip.ParseAddr(rawHost); err == nil {
		// IPv6 zone names identify interfaces and are case-sensitive.
		host = addr.String()
	} else {
		host = strings.ToLower(rawHost)
	}
	port = u.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", "", false
		}
		port = strconv.Itoa(n)
	}
	return scheme, host, port, true
}
