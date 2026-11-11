package app

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

// TokenFileEnvVar names the file holding the bearer token that HTTP clients
// must send.
const TokenFileEnvVar = "KS_MCP_SERVER_TOKEN_FILE"

var tokenPattern = regexp.MustCompile(`^[0-9a-f]{64}\n?$`)

// loadToken reads the bearer token. The file must be a regular file (not a
// symlink) owned by this user, mode 0600, holding 64 lowercase hex characters
// and an optional trailing newline.
func loadToken(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%s is not set", TokenFileEnvVar)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("token file: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("token file %s is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("token file %s is not a regular file", path)
	}
	if !ownedByMe(fi) {
		return nil, fmt.Errorf("token file %s is not owned by this user", path)
	}
	if fi.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("token file %s must have mode 0600, has %04o", path, fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("token file: %w", err)
	}
	if !tokenPattern.Match(b) {
		return nil, fmt.Errorf("token file %s must hold 64 lowercase hex characters", path)
	}
	return []byte(strings.TrimSuffix(string(b), "\n")), nil
}

// ownedByMe reads the Uid field of the platform stat struct; reflection keeps
// this compiling where syscall.Stat_t does not exist, and refuses there.
func ownedByMe(fi os.FileInfo) bool {
	v := reflect.Indirect(reflect.ValueOf(fi.Sys()))
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return false
	}
	uid := v.FieldByName("Uid")
	return uid.IsValid() && uid.CanUint() && os.Getuid() >= 0 && uid.Uint() == uint64(os.Getuid())
}

// loopbackOnly refuses any listen address whose host is not a loopback IP,
// including an empty host, which would bind every interface.
func loopbackOnly(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q is not a loopback IP such as 127.0.0.1", addr)
	}
	return nil
}

// guard admits a request only without an Origin header, with Host exactly
// 127.0.0.1:<port> or localhost:<port>, and with the bearer token. The token
// file is re-read when its mtime changes, so a rotated token needs no restart;
// an unreadable or invalid file rejects every request until it is fixed.
func guard(next http.Handler, tokenPath string, port string) http.Handler {
	hosts := map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true}
	var (
		mu    sync.Mutex
		token []byte
		mtime time.Time
	)
	current := func() []byte {
		mu.Lock()
		defer mu.Unlock()
		fi, err := os.Stat(tokenPath)
		if err != nil {
			if token != nil {
				log.Printf("token file: %v", err)
			}
			token, mtime = nil, time.Time{}
			return nil
		}
		if token == nil || !fi.ModTime().Equal(mtime) {
			t, err := loadToken(tokenPath)
			if err != nil {
				log.Print(err)
			}
			token, mtime = t, fi.ModTime()
		}
		return token
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Origin"]; ok {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !hosts[r.Host] {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		want := current()
		if !ok || want == nil || len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// preflight refuses a non-loopback listen address or an invalid token file,
// before anything else starts.
func preflight(addr string) error {
	if err := loopbackOnly(addr); err != nil {
		return err
	}
	_, err := loadToken(os.Getenv(TokenFileEnvVar))
	return err
}

// serveGuarded serves h on the loopback address addr behind guard.
func serveGuarded(addr string, h http.Handler) error {
	if err := preflight(addr); err != nil {
		return err
	}
	_, port, _ := net.SplitHostPort(addr)
	tokenPath := os.Getenv(TokenFileEnvVar)
	srv := &http.Server{Addr: addr, Handler: guard(h, tokenPath, port), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}
