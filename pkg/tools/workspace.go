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
		tool(ks, newTool("list_workspaces", "List workspaces: {items, totalItems}, paged. spec.template.spec.manager is the administrator; spec.placement.clusters are the clusters its projects live on.", pagingOptions("workspaces")...),
			func(a args) (string, url.Values, error) {
				p, err := kubesphere.KAPIPath("", constants.TenantV1alpha3, "workspacetemplates")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_workspace", "Get one workspace: spec.template.spec.manager is the administrator; spec.placement.clusters are the clusters its projects live on.", mcp.WithString("workspaceName", mcp.Description("the given workspaceName"), mcp.Required())),
			func(a args) (string, url.Values, error) {
				ws, err := a.need("workspaceName")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.TenantV1alpha3, "workspacetemplates", ws)
				return p, nil, err
			}),

		tool(ks, newTool("list_workspace_members", "List a workspace's members: {items, totalItems}, paged. metadata.name is the username; annotation iam.kubesphere.io/workspacerole is the role.", append(pagingOptions("workspace members"),
			mcp.WithString("workspaceName", mcp.Description("the given workspaceName"), mcp.Required()))...),
			func(a args) (string, url.Values, error) {
				ws, err := a.need("workspaceName")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.IAMV1alpha2, "workspaces", ws, "workspacemembers")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_workspace_quota", "Get a workspace's resource quota on one cluster: spec.quota.hard (limits) and status.total.used (usage). A 404 means the workspace has no quota there.",
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

		tool(ks, newTool("list_project_members", "List a project's members: {items, totalItems}, paged. metadata.name is the username; annotation iam.kubesphere.io/role is the role.", append(pagingOptions("project members"),
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
