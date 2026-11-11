package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tokenA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tokenB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func writeToken(t *testing.T, dir, token string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(path, []byte(token+"\n"), mode))
	require.NoError(t, os.Chmod(path, mode))
	return path
}

var reached = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

func send(h http.Handler, host, bearer string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://"+host+"/mcp", strings.NewReader("{}"))
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGuardPassesAnAuthorizedRequest(t *testing.T) {
	h := guard(reached, writeToken(t, t.TempDir(), tokenA, 0o600), "47814")
	assert.Equal(t, http.StatusTeapot, send(h, "127.0.0.1:47814", tokenA, nil).Code)
	assert.Equal(t, http.StatusTeapot, send(h, "localhost:47814", tokenA, nil).Code)
}

func TestGuardRejectsAnyOrigin(t *testing.T) {
	h := guard(reached, writeToken(t, t.TempDir(), tokenA, 0o600), "47814")
	w := send(h, "127.0.0.1:47814", tokenA, map[string]string{"Origin": "http://127.0.0.1:47814"})
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGuardRejectsAForeignHost(t *testing.T) {
	h := guard(reached, writeToken(t, t.TempDir(), tokenA, 0o600), "47814")
	for _, host := range []string{"evil.example:47814", "127.0.0.1:1", "[::1]:47814", "127.0.0.1"} {
		assert.Equal(t, http.StatusForbidden, send(h, host, tokenA, nil).Code, host)
	}
}

func TestGuardMissingOrWrongTokenIsPlain401(t *testing.T) {
	h := guard(reached, writeToken(t, t.TempDir(), tokenA, 0o600), "47814")
	for _, bearer := range []string{"", tokenB, tokenA[:10]} {
		w := send(h, "127.0.0.1:47814", bearer, nil)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Empty(t, w.Header().Values("WWW-Authenticate"))
		assert.Empty(t, w.Body.String())
	}
}

func TestGuardReloadsARotatedToken(t *testing.T) {
	dir := t.TempDir()
	path := writeToken(t, dir, tokenA, 0o600)
	h := guard(reached, path, "47814")
	require.Equal(t, http.StatusTeapot, send(h, "127.0.0.1:47814", tokenA, nil).Code)

	writeToken(t, dir, tokenB, 0o600)
	later := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(path, later, later))
	assert.Equal(t, http.StatusTeapot, send(h, "127.0.0.1:47814", tokenB, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, send(h, "127.0.0.1:47814", tokenA, nil).Code)
}

func TestGuardRejectsEverythingWhenTheTokenFileTurnsInvalid(t *testing.T) {
	dir := t.TempDir()
	path := writeToken(t, dir, tokenA, 0o600)
	h := guard(reached, path, "47814")
	require.Equal(t, http.StatusTeapot, send(h, "127.0.0.1:47814", tokenA, nil).Code)

	require.NoError(t, os.Chmod(path, 0o644))
	later := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(path, later, later))
	assert.Equal(t, http.StatusUnauthorized, send(h, "127.0.0.1:47814", tokenA, nil).Code)
}

func TestLoadTokenRules(t *testing.T) {
	dir := t.TempDir()
	good := writeToken(t, dir, tokenA, 0o600)
	got, err := loadToken(good)
	require.NoError(t, err)
	assert.Equal(t, tokenA, string(got))

	t.Run("group readable", func(t *testing.T) {
		_, err := loadToken(writeToken(t, t.TempDir(), tokenA, 0o644))
		assert.ErrorContains(t, err, "0600")
	})
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(good, link))
		_, err := loadToken(link)
		assert.ErrorContains(t, err, "symlink")
	})
	t.Run("not hex", func(t *testing.T) {
		_, err := loadToken(writeToken(t, t.TempDir(), strings.ToUpper(tokenA), 0o600))
		assert.ErrorContains(t, err, "64 lowercase hex")
	})
	t.Run("directory", func(t *testing.T) {
		_, err := loadToken(t.TempDir())
		assert.ErrorContains(t, err, "regular file")
	})
	t.Run("unset", func(t *testing.T) {
		_, err := loadToken("")
		assert.ErrorContains(t, err, TokenFileEnvVar)
	})
}

func TestLoopbackOnly(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:1", ":3000", "localhost:3000", "192.168.1.2:3000", "127.0.0.1"} {
		assert.Error(t, loopbackOnly(addr), addr)
	}
	assert.NoError(t, loopbackOnly("127.0.0.1:47814"))
}

// An authorized request goes through the guard to the stateless MCP endpoint.
func TestGuardServesStreamableHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/mcp", server.NewStreamableHTTPServer(server.NewMCPServer("KubeSphere", "test", server.WithToolCapabilities(false)),
		server.WithStateLess(true), server.WithEndpointPath("/mcp")))
	h := guard(mux, writeToken(t, t.TempDir(), tokenA, 0o600), "47814")

	r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:47814/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Authorization", "Bearer "+tokenA)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"tools"`)
	assert.Empty(t, w.Header().Get("Mcp-Session-Id"))
}

func TestSSERefusesANonLoopbackAddress(t *testing.T) {
	t.Setenv(TokenFileEnvVar, writeToken(t, t.TempDir(), tokenA, 0o600))
	for _, address := range []string{"0.0.0.0", "", "192.168.1.2"} {
		cmd := newSSECommand()
		cmd.SetArgs([]string{"--address", address, "--port", "1"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		require.Error(t, err, address)
		assert.Contains(t, err.Error(), "loopback", address)
	}
}

func TestSSEWithoutTokenIs401(t *testing.T) {
	h := guard(server.NewSSEServer(server.NewMCPServer("KubeSphere", "test")), writeToken(t, t.TempDir(), tokenA, 0o600), "3000")
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3000/sse", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Empty(t, w.Header().Values("WWW-Authenticate"))
}

func TestSSEWithTokenOpensTheStream(t *testing.T) {
	h := guard(server.NewSSEServer(server.NewMCPServer("KubeSphere", "test")), writeToken(t, t.TempDir(), tokenA, 0o600), "3000")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:3000/sse", nil)
	r.Header.Set("Authorization", "Bearer "+tokenA)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "event: endpoint")
}
