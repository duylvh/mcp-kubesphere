package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

// A 403 on a cluster-wide list points to the per-project call; a 403 inside
// one project does not, since passing project again cannot help.
func TestForbiddenListSuggestsProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"pods is forbidden"}`))
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "session.json")
	session := fmt.Sprintf(`{"sessions":{%q:{"value":"token=t","expires":%d,"updatedAt":1}}}`, srv.URL, time.Now().Add(time.Hour).Unix())
	require.NoError(t, os.WriteFile(path, []byte(session), 0o600))
	ks, err := kubesphere.NewKSConfig(kubesphere.Options{APIServer: srv.URL, CookieFile: path})
	require.NoError(t, err)
	tools := byName(ks)

	res := call(t, tools, "list_pods", map[string]any{"cluster": "prod-3"})
	assert.True(t, res.IsError)
	assert.Contains(t, text(res), "report it, do not retry")
	assert.Contains(t, text(res), "pass project to list one project you can read")

	res = call(t, tools, "list_pods", map[string]any{"cluster": "prod-3", "project": "p1"})
	assert.True(t, res.IsError)
	assert.NotContains(t, text(res), "pass project")
}

func TestMissingPodSaysWhereToFindIt(t *testing.T) {
	_, ks := newRecorder(t)
	res := call(t, byName(ks), "get_pod_logs", map[string]any{"cluster": "dev", "project": "p1"})
	assert.True(t, res.IsError)
	assert.Contains(t, text(res), "from list_pods with name or labelSelector app=<deployment>")
}
