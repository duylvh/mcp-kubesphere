package kubesphere

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
)

// requestTimeout bounds waiting for KubeSphere to answer. In cookie mode it
// applies per request hop, so a session refresh running between the first
// attempt and the retry (up to cookieRefreshTimeout) is not cut short.
var requestTimeout = 60 * time.Second

const (
	// URLEnvVar names the KubeSphere console or ks-apiserver address.
	URLEnvVar = "KS_URL"

	maxResponseSize = 32 << 20
	maxErrorMessage = 300
)

// Options selects how the server reaches and authenticates to KubeSphere.
// CookieFile takes precedence; otherwise KSConfig must hold a bearer token.
type Options struct {
	// KSConfig is a kubeconfig-format file holding the server address, an
	// optional CA and a bearer token.
	KSConfig string
	// APIServer overrides the address from KSConfig, or names it in cookie mode.
	APIServer string
	// CookieFile is an absolute path to an origin-keyed session file.
	CookieFile string
	// CookieRefreshCommand renews CookieFile; it requires CookieFile.
	CookieRefreshCommand []string
}

// OptionsFromEnv fills cookie-mode settings from KS_URL, KS_COOKIE_FILE and
// KS_COOKIE_REFRESH_COMMAND on top of the command-line values.
func OptionsFromEnv(ksconfig, apiserver string) (Options, error) {
	o := Options{KSConfig: ksconfig, APIServer: apiserver, CookieFile: os.Getenv(CookieFileEnvVar)}
	if v := os.Getenv(URLEnvVar); v != "" {
		o.APIServer = v
	}
	if raw := os.Getenv(CookieRefreshCommandEnvVar); raw != "" {
		if o.CookieFile == "" {
			return Options{}, fmt.Errorf("%s requires %s", CookieRefreshCommandEnvVar, CookieFileEnvVar)
		}
		argv, err := ParseCookieRefreshCommand(raw)
		if err != nil {
			return Options{}, err
		}
		o.CookieRefreshCommand = argv
	}
	return o, nil
}

// KSConfig sends authenticated GET requests to the KubeSphere API.
type KSConfig struct {
	httpClient *http.Client
	baseURL    *url.URL
}

// NewKSConfig builds a client for cookie mode when o.CookieFile is set, and
// for bearer-token mode otherwise.
func NewKSConfig(o Options) (*KSConfig, error) {
	var (
		base          string
		transport     http.RoundTripper
		clientTimeout time.Duration
	)
	if o.CookieFile != "" {
		if o.APIServer == "" {
			return nil, fmt.Errorf("%s requires the KubeSphere address in %s or --ks-apiserver", CookieFileEnvVar, URLEnvVar)
		}
		base = o.APIServer
		hop := http.DefaultTransport.(*http.Transport).Clone()
		hop.ResponseHeaderTimeout = requestTimeout
		t, err := newSessionCookieTransport(hop, o.CookieFile, base, o.CookieRefreshCommand)
		if err != nil {
			return nil, err
		}
		transport = t
	} else {
		if len(o.CookieRefreshCommand) > 0 {
			return nil, fmt.Errorf("a cookie refresh command requires %s", CookieFileEnvVar)
		}
		config, err := loadKSConfig(o.KSConfig)
		if err != nil {
			return nil, err
		}
		if config.BearerToken == "" && config.BearerTokenFile == "" {
			return nil, fmt.Errorf("the ksconfig must hold a bearer token (users[].user.token), or set %s to use a browser session", CookieFileEnvVar)
		}
		base = config.Host
		if o.APIServer != "" {
			base = o.APIServer
		}
		config.Host = base
		rt, err := restclient.TransportFor(config)
		if err != nil {
			return nil, err
		}
		transport = rt
		clientTimeout = requestTimeout
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the KubeSphere address must be an absolute http(s) URL")
	}
	return &KSConfig{
		baseURL: u,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   clientTimeout,
			// API routes never redirect; a redirect means the session is not
			// accepted, and following it would return a sign-in page as data.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func loadKSConfig(path string) (*restclient.Config, error) {
	if path == "" {
		return nil, fmt.Errorf("pass --ksconfig with a bearer token, or set %s", CookieFileEnvVar)
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: path},
		&clientcmd.ConfigOverrides{CurrentContext: constants.Getenv(constants.Kubesphere)},
	).ClientConfig()
}

// KAPIPath builds "/kapis[/clusters/{cluster}]/{groupVersion}/{segments...}".
// An empty cluster addresses the cluster that serves the API (the host
// cluster). Each segment is a single path element; user-supplied names that
// could change the path, such as "..", are rejected.
func KAPIPath(cluster, groupVersion string, segments ...string) (string, error) {
	return buildPath("/kapis", cluster, groupVersion, segments...)
}

// CoreV1Path builds "/api/clusters/{cluster}/v1/{segments...}", the Kubernetes
// core API that ks-apiserver proxies to a member cluster, for objects the
// /kapis API does not serve, such as pod logs and events. The cluster is
// required: the host form "/api/v1" is a different cluster.
func CoreV1Path(cluster string, segments ...string) (string, error) {
	if cluster == "" {
		return "", errors.New(`argument "cluster" is required`)
	}
	return buildPath("/api", cluster, "v1", segments...)
}

func buildPath(prefix, cluster, groupVersion string, segments ...string) (string, error) {
	var b strings.Builder
	b.WriteString(prefix)
	if cluster != "" {
		if err := checkSegment("cluster", cluster); err != nil {
			return "", err
		}
		b.WriteString("/clusters/")
		b.WriteString(url.PathEscape(cluster))
	}
	b.WriteString("/")
	b.WriteString(groupVersion)
	for _, s := range segments {
		if err := checkSegment("name", s); err != nil {
			return "", err
		}
		b.WriteString("/")
		b.WriteString(url.PathEscape(s))
	}
	return b.String(), nil
}

func checkSegment(kind, s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\?#%") || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("invalid %s %q", kind, s)
	}
	return nil
}

// Get requests path (from KAPIPath or CoreV1Path) with query and returns the
// body: JSON, or plain text for pod logs.
func (c *KSConfig) Get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	// path is already escaped by KAPIPath or CoreV1Path.
	target := c.baseURL.String() + path
	if q := query.Encode(); q != "" {
		target += "?" + q
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading KubeSphere response for %s: %w", path, err)
	}
	if len(body) > maxResponseSize {
		return nil, fmt.Errorf("KubeSphere response for %s exceeds %d MiB; use a smaller limit", path, maxResponseSize>>20)
	}
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		loc, _ := req.URL.Parse(resp.Header.Get("Location"))
		return nil, fmt.Errorf("KubeSphere redirected %s to %s; the session is not signed in", path, redirectTarget(loc))
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := "(HTML page)"
		if mediaType != "text/html" {
			msg = errorMessage(body)
		}
		return nil, fmt.Errorf("KubeSphere returned %d for %s: %s", resp.StatusCode, path, msg)
	}
	if mediaType == "text/html" {
		return nil, fmt.Errorf("KubeSphere returned an HTML page instead of JSON for %s; this address does not serve that API path", path)
	}
	return body, nil
}

func redirectTarget(u *url.URL) string {
	if u == nil {
		return "<unknown>"
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// errorMessage extracts the "message" of a Kubernetes Status body, or a short
// prefix of a plain-text body.
func errorMessage(body []byte) string {
	var status struct {
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &status) == nil && status.Message != "" {
		msg = status.Message
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > maxErrorMessage {
		msg = msg[:maxErrorMessage] + "…"
	}
	if msg == "" {
		return "(empty body)"
	}
	return msg
}
