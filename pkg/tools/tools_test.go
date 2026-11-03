package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

// recorder is a fake KubeSphere that records each request URI and answers
// with the body registered for its path, or an empty list.
type recorder struct {
	*httptest.Server
	mu     sync.Mutex
	uris   []string
	bodies map[string]string
}

func newRecorder(t *testing.T) (*recorder, *kubesphere.KSConfig) {
	t.Helper()
	r := &recorder{bodies: map[string]string{}}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.uris = append(r.uris, req.URL.RequestURI())
		body, ok := r.bodies[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			body = `{"items":[],"totalItems":0}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(r.Close)
	path := filepath.Join(t.TempDir(), "session.json")
	session := fmt.Sprintf(`{"sessions":{%q:{"value":"token=t","expires":%d,"updatedAt":1}}}`, r.URL, time.Now().Add(time.Hour).Unix())
	require.NoError(t, os.WriteFile(path, []byte(session), 0o600))
	ks, err := kubesphere.NewKSConfig(kubesphere.Options{APIServer: r.URL, CookieFile: path})
	require.NoError(t, err)
	return r, ks
}

func (r *recorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.uris) == 0 {
		return ""
	}
	return r.uris[len(r.uris)-1]
}

func call(t *testing.T, tools map[string]server.ServerTool, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool, ok := tools[name]
	require.True(t, ok, "tool %s is registered", name)
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = arguments
	res, err := tool.Handler(t.Context(), req)
	require.NoError(t, err)
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func byName(ks *kubesphere.KSConfig) map[string]server.ServerTool {
	m := map[string]server.ServerTool{}
	for _, t := range All(ks) {
		m[t.Tool.Name] = t
	}
	return m
}

func TestToolSet(t *testing.T) {
	_, ks := newRecorder(t)
	tools := byName(ks)
	assert.Len(t, tools, 37)
	assert.Len(t, All(ks), 37, "no tool is registered twice")
	for _, removed := range []string{"list_extensions", "list_permissions"} {
		assert.NotContains(t, tools, removed, "4.x-only tool %s is not offered", removed)
	}
}

// TestToolPaths pins every tool to the KubeSphere v3.3 route it calls. Member
// clusters must use /kapis/clusters/{c}/: the console answers the 4.x form
// /clusters/{c}/kapis/ with its HTML page.
func TestToolPaths(t *testing.T) {
	const paging = "?limit=10&page=1&sortBy="
	const opPaging = "?orderBy=createTime&paging=limit%3D10%2Cpage%3D1&reverse=true"
	nsArgs := map[string]any{"cluster": "dev", "project": "team-project"}
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list_users", nil, "/kapis/iam.kubesphere.io/v1alpha2/users" + paging + "createTime"},
		{"get_user", map[string]any{"user": "alice"}, "/kapis/iam.kubesphere.io/v1alpha2/users/alice"},
		{"list_roles", map[string]any{"level": "global"}, "/kapis/iam.kubesphere.io/v1alpha2/globalroles" + paging + "createTime"},
		{"list_roles", map[string]any{"level": "cluster", "cluster": "dev"}, "/kapis/clusters/dev/iam.kubesphere.io/v1alpha2/clusterroles" + paging + "createTime"},
		{"list_roles", map[string]any{"level": "workspace", "workspace": "team-ws"}, "/kapis/iam.kubesphere.io/v1alpha2/workspaces/team-ws/workspaceroles" + paging + "createTime"},
		{"list_roles", map[string]any{"level": "namespace", "cluster": "dev", "project": "p1"}, "/kapis/clusters/dev/iam.kubesphere.io/v1alpha2/namespaces/p1/roles" + paging + "createTime"},
		{"get_role", map[string]any{"level": "global", "rolename": "platform-admin"}, "/kapis/iam.kubesphere.io/v1alpha2/globalroles/platform-admin"},
		{"get_role", map[string]any{"level": "cluster", "cluster": "dev", "rolename": "cluster-viewer"}, "/kapis/clusters/dev/iam.kubesphere.io/v1alpha2/clusterroles/cluster-viewer"},
		{"get_role", map[string]any{"level": "workspace", "workspace": "team-ws", "rolename": "team-viewer"}, "/kapis/iam.kubesphere.io/v1alpha2/workspaces/team-ws/workspaceroles/team-viewer"},
		{"get_role", map[string]any{"level": "namespace", "cluster": "dev", "project": "p1", "rolename": "viewer"}, "/kapis/clusters/dev/iam.kubesphere.io/v1alpha2/namespaces/p1/roles/viewer"},

		{"list_workspaces", nil, "/kapis/tenant.kubesphere.io/v1alpha3/workspacetemplates" + paging + "createTime"},
		{"get_workspace", map[string]any{"workspaceName": "team-ws"}, "/kapis/tenant.kubesphere.io/v1alpha3/workspacetemplates/team-ws"},
		{"list_workspace_members", map[string]any{"workspaceName": "team-ws"}, "/kapis/iam.kubesphere.io/v1alpha2/workspaces/team-ws/workspacemembers" + paging + "createTime"},
		{"get_workspace_quota", map[string]any{"workspaceName": "team-ws", "cluster": "dev"}, "/kapis/clusters/dev/tenant.kubesphere.io/v1alpha2/workspaces/team-ws/resourcequotas/team-ws"},
		{"list_project_members", nsArgs, "/kapis/clusters/dev/iam.kubesphere.io/v1alpha2/namespaces/team-project/members" + paging + "createTime"},

		{"list_application_repos", map[string]any{"workspaceName": "team-ws"}, "/kapis/openpitrix.io/v1/workspaces/team-ws/repos" + opPaging},
		{"list_applications", map[string]any{"workspace": "team-ws", "cluster": "dev", "project": "p1"}, "/kapis/openpitrix.io/v1/workspaces/team-ws/clusters/dev/namespaces/p1/applications" + opPaging},
		{"get_application", map[string]any{"workspace": "team-ws", "cluster": "dev", "project": "p1", "applicationName": "app-1"}, "/kapis/openpitrix.io/v1/workspaces/team-ws/clusters/dev/namespaces/p1/applications/app-1"},
		{"get_application_version", map[string]any{"applicationName": "app-1"}, "/kapis/openpitrix.io/v1/apps/app-1/versions" + opPaging},

		{"list_clusters", nil, "/kapis/resources.kubesphere.io/v1alpha3/clusters" + paging + "createTime"},
		{"get_cluster", map[string]any{"cluster": "dev"}, "/kapis/resources.kubesphere.io/v1alpha3/clusters/dev"},
		{"get_cluster_tags", nil, "/kapis/resources.kubesphere.io/v1alpha3/clusters"},
		{"list_cluster_members", map[string]any{"cluster": "dev"}, "/kapis/clusters/dev/iam.kubesphere.io/v1alpha2/clustermembers" + paging + "createTime"},
		{"list_projects", map[string]any{"cluster": "dev"}, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/namespaces" + paging + "name"},
		{"list_projects", map[string]any{"cluster": "dev", "workspace": "team-ws"}, "/kapis/clusters/dev/tenant.kubesphere.io/v1alpha2/workspaces/team-ws/namespaces" + paging + "createTime"},

		{"get_pod", map[string]any{"cluster": "dev", "project": "p1", "pod": "svc-0"}, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/namespaces/p1/pods/svc-0"},

		// Logs and events come from the Kubernetes core API that ks-apiserver
		// proxies per cluster; the host form /api/v1 is a different cluster.
		{"get_pod_logs", map[string]any{"cluster": "dev", "project": "p1", "pod": "svc-0"}, "/api/clusters/dev/v1/namespaces/p1/pods/svc-0/log?tailLines=100"},
		{"list_events", nsArgs, "/api/clusters/dev/v1/namespaces/team-project/events"},
	}
	for _, r := range clusterResources {
		cases = append(cases, struct {
			tool string
			args map[string]any
			want string
		}{"list_" + r, map[string]any{"cluster": "dev"}, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/" + r + paging + "name"})
	}
	for _, r := range projectResources {
		cases = append(cases,
			struct {
				tool string
				args map[string]any
				want string
			}{"list_" + r, nsArgs, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/namespaces/team-project/" + r + paging + "name"},
			struct {
				tool string
				args map[string]any
				want string
			}{"list_" + r, map[string]any{"cluster": "host"}, "/kapis/clusters/host/resources.kubesphere.io/v1alpha3/" + r + paging + "name"},
		)
	}

	rec, ks := newRecorder(t)
	tools := byName(ks)
	covered := map[string]bool{}
	for _, tc := range cases {
		res := call(t, tools, tc.tool, tc.args)
		require.False(t, res.IsError, "%s %v: %s", tc.tool, tc.args, text(res))
		assert.Equal(t, tc.want, rec.last(), "%s %v", tc.tool, tc.args)
		covered[tc.tool] = true
	}
	for name := range tools {
		assert.True(t, covered[name], "tool %s has a path case", name)
	}
}

func TestToolArguments(t *testing.T) {
	rec, ks := newRecorder(t)
	tools := byName(ks)

	call(t, tools, "list_pods", map[string]any{"cluster": "dev", "limit": float64(50), "page": float64(3)})
	assert.Equal(t, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/pods?limit=50&page=3&sortBy=name", rec.last(), "JSON numbers set paging")

	call(t, tools, "list_pods", map[string]any{"cluster": "dev", "limit": float64(-1), "page": 2.5})
	assert.Equal(t, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/pods?limit=10&page=1&sortBy=name", rec.last(), "invalid paging falls back to defaults")

	before := len(rec.uris)
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"list_pods", nil, `argument "cluster" is required`},
		{"get_user", map[string]any{"user": 7}, `argument "user" is required`},
		{"list_roles", map[string]any{"level": "platform"}, "level must be one of"},
		{"list_roles", map[string]any{"level": "workspace"}, `argument "workspace" is required`},
		{"get_user", map[string]any{"user": "../../config.kubesphere.io/v1alpha2/configs/configz"}, "invalid name"},
		{"list_pods", map[string]any{"cluster": "dev", "project": ".."}, "invalid name"},
		{"list_pods", map[string]any{"cluster": "../x"}, "invalid cluster"},
	} {
		res := call(t, tools, tc.tool, tc.args)
		assert.True(t, res.IsError, "%s %v", tc.tool, tc.args)
		assert.Contains(t, text(res), tc.want)
	}
	assert.Len(t, rec.uris, before, "invalid arguments never reach KubeSphere")
}

func TestResourceFilters(t *testing.T) {
	rec, ks := newRecorder(t)
	tools := byName(ks)

	call(t, tools, "list_pods", map[string]any{"cluster": "prod-3", "project": "team-project", "labelSelector": "app=svc,tier in (api)"})
	assert.Equal(t, "/kapis/clusters/prod-3/resources.kubesphere.io/v1alpha3/namespaces/team-project/pods?labelSelector=app%3Dsvc%2Ctier+in+%28api%29&limit=10&page=1&sortBy=name", rec.last())

	call(t, tools, "list_deployments", map[string]any{"cluster": "prod-3", "project": "team-project", "name": "svc"})
	assert.Equal(t, "/kapis/clusters/prod-3/resources.kubesphere.io/v1alpha3/namespaces/team-project/deployments?limit=10&name=svc&page=1&sortBy=name", rec.last())

	call(t, tools, "list_nodes", map[string]any{"cluster": "dev", "labelSelector": "node-role.kubernetes.io/worker"})
	assert.Equal(t, "/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/nodes?labelSelector=node-role.kubernetes.io%2Fworker&limit=10&page=1&sortBy=name", rec.last())
}

func TestListsAreCompacted(t *testing.T) {
	rec, ks := newRecorder(t)
	rec.bodies["/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/namespaces/p1/deployments"] = `{"items":[{"metadata":{"name":"svc","managedFields":[{"manager":"m"}],"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{\"env\":\"S3CRET\"}","keep":"yes"}},"spec":{"replicas":2}}],"totalItems":1}`
	out := text(call(t, byName(ks), "list_deployments", map[string]any{"cluster": "dev", "project": "p1"}))
	assert.NotContains(t, out, "managedFields")
	assert.NotContains(t, out, "S3CRET")
	assert.JSONEq(t, `{"items":[{"metadata":{"name":"svc","annotations":{"keep":"yes"}},"spec":{"replicas":2}}],"totalItems":1}`, out)
}

func TestClusterTags(t *testing.T) {
	rec, ks := newRecorder(t)
	rec.bodies["/kapis/resources.kubesphere.io/v1alpha3/clusters"] = `{"items":[
		{"metadata":{"name":"host","labels":{"cluster-role.kubesphere.io/host":"","env":"dev"}}},
		{"metadata":{"name":"dev","labels":{"env":"dev"}}},
		{"metadata":{"name":"prod-3","labels":{"env":"prod"}}}],"totalItems":3}`
	res := call(t, byName(ks), "get_cluster_tags", nil)
	require.False(t, res.IsError, text(res))
	assert.JSONEq(t, `{"cluster-role.kubesphere.io/host":{"":["host"]},"env":{"dev":["dev","host"],"prod":["prod-3"]}}`, text(res))
}

func TestListSecretsIsRedactedEndToEnd(t *testing.T) {
	rec, ks := newRecorder(t)
	rec.bodies["/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/namespaces/p1/secrets"] = secretListFixture
	res := call(t, byName(ks), "list_secrets", map[string]any{"cluster": "dev", "project": "p1"})
	require.False(t, res.IsError, text(res))
	assertNoSecretValues(t, text(res))
}

func TestClusterCredentialsAreStripped(t *testing.T) {
	rec, ks := newRecorder(t)
	cluster := `{"metadata":{"name":"edge"},"spec":{"connection":{"type":"proxy","token":"S3CRET-agent","kubeconfig":"UzNDUkVU","kubernetesAPIEndpoint":"https://10.0.0.1:6443"}}}`
	rec.bodies["/kapis/resources.kubesphere.io/v1alpha3/clusters"] = `{"items":[` + cluster + `],"totalItems":1}`
	rec.bodies["/kapis/resources.kubesphere.io/v1alpha3/clusters/edge"] = cluster
	tools := byName(ks)
	for name, args := range map[string]map[string]any{"list_clusters": nil, "get_cluster": {"cluster": "edge"}} {
		out := text(call(t, tools, name, args))
		assert.NotContains(t, out, "S3CRET", name)
		assert.NotContains(t, out, "UzNDUkVU", name)
		assert.Contains(t, out, `"type":"proxy"`, name)
		assert.Contains(t, out, "kubernetesAPIEndpoint", name)
	}
}
