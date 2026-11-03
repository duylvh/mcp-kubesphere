package tools

import (
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

func workspaceTools(ks *kubesphere.KSConfig) []server.ServerTool {
	return []server.ServerTool{
		tool(ks, newTool("list_workspaces", `
Retrieve the paginated workspace list. The response will include:
1. items: An array of workspace template objects containing:
   - workspaceName: Maps to metadata.name.
   - administrator: Maps to spec.template.spec.manager. indicates the workspace's administrator.
   - clusters: Maps to spec.placement.clusters. the clusters to which the workspace is assigned; its projects live there.
2. totalItems: The total number of workspaces in KubeSphere.
`, pagingOptions("workspaces")...),
			func(a args) (string, url.Values, error) {
				p, err := kubesphere.KAPIPath("", constants.TenantV1alpha3, "workspacetemplates")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_workspace", `
Get workspace information. The response will contain:
- workspaceName: Maps to metadata.name.
- administrator: Maps to spec.template.spec.manager. indicates the workspace's administrator.
- clusters: Maps to spec.placement.clusters. the clusters to which the workspace is assigned; its projects live there.
`, mcp.WithString("workspaceName", mcp.Description("the given workspaceName"), mcp.Required())),
			func(a args) (string, url.Values, error) {
				ws, err := a.need("workspaceName")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.TenantV1alpha3, "workspacetemplates", ws)
				return p, nil, err
			}),

		tool(ks, newTool("list_workspace_members", `
Retrieve the paginated workspace members list. The response will include:
1. items: An array of workspace member objects containing:
  - username: Maps to metadata.name
  - specific metadata.annotations fields indicate:
   - iam.kubesphere.io/workspacerole: the workspace role which this user belong to.
2. totalItems: The total number of workspace members in KubeSphere.
`, append(pagingOptions("workspace members"),
			mcp.WithString("workspaceName", mcp.Description("the given workspaceName"), mcp.Required()))...),
			func(a args) (string, url.Values, error) {
				ws, err := a.need("workspaceName")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.IAMV1alpha2, "workspaces", ws, "workspacemembers")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_workspace_quota", `
Get the resource quota of a workspace on one cluster. The response will contain:
- spec.quota.hard: the limits of the workspace, e.g. limits.cpu, limits.memory, requests.cpu, requests.memory, count/pods.
- status.total.used: the current usage.
A 404 means the workspace has no quota on that cluster.
`,
			mcp.WithString("workspaceName", mcp.Description("the given workspaceName"), mcp.Required()),
			clusterOption(true),
		),
			func(a args) (string, url.Values, error) {
				ws, err := a.need("workspaceName")
				if err != nil {
					return "", nil, err
				}
				cluster, err := a.need("cluster")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath(cluster, constants.TenantV1alpha2, "workspaces", ws, "resourcequotas", ws)
				return p, nil, err
			}),

		tool(ks, newTool("list_project_members", `
Retrieve the paginated project members list. The response will include:
1. items: An array of project member objects in project containing:
  - username: Maps to metadata.name
  - specific metadata.annotations fields indicate:
   - iam.kubesphere.io/role: the project role which this user belong to.
2. totalItems: The total number of project members in KubeSphere.
`, append(pagingOptions("project members"),
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the given projectName"), mcp.Required()))...),
			func(a args) (string, url.Values, error) {
				cluster, err := a.need("cluster")
				if err != nil {
					return "", nil, err
				}
				project, err := a.need("project")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath(cluster, constants.IAMV1alpha2, "namespaces", project, "members")
				return p, a.paging("createTime"), err
			}),
	}
}
