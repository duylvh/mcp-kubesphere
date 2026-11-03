package tools

import (
	"fmt"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

func iamTools(ks *kubesphere.KSConfig) []server.ServerTool {
	return []server.ServerTool{
		tool(ks, newTool("list_users", `
Retrieve the paginated users list. The response will include:
1. items: An array of user objects containing:
  - username: Maps to metadata.name
  - email: Maps to spec.email
  - specific metadata.annotations fields indicate:
   - iam.kubesphere.io/globalrole: the platform role of the user.
   - iam.kubesphere.io/last-login-time: the last login time of the user.
2. totalItems: The total number of users in KubeSphere.
`, pagingOptions("users")...),
			func(a args) (string, url.Values, error) {
				p, err := kubesphere.KAPIPath("", constants.IAMV1alpha2, "users")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_user", `
Get user information. The response will contain:
- username: Maps to metadata.name
- email: Maps to spec.email
- specific metadata.annotations fields indicate:
 - iam.kubesphere.io/globalrole: the platform role of the user.
`, mcp.WithString("user", mcp.Description("the given username"), mcp.Required())),
			func(a args) (string, url.Values, error) {
				user, err := a.need("user")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.IAMV1alpha2, "users", user)
				return p, nil, err
			}),

		tool(ks, newTool("list_roles", `
Retrieve the paginated roles list of one level. The response will include:
1. items: An array of role objects containing:
  - roleName: Maps to metadata.name
  - rules: the Kubernetes RBAC rules of the role.
  - specific metadata.annotations fields indicate:
   - iam.kubesphere.io/aggregation-roles: the role templates the role aggregates.
2. totalItems: The total number of roles in KubeSphere.
A normal user may get 403 for platform or workspace roles; that is RBAC, not an error in this tool.
`, append(pagingOptions("roles"),
			mcp.WithString("level", mcp.Description("role level. it's four level: global (platform-level), cluster (cluster-level), workspace (workspace-level), namespace (project-level)"), mcp.Required()),
			mcp.WithString("cluster", mcp.Description("the given clusterName which role belong to. require when level is cluster or namespace.")),
			mcp.WithString("workspace", mcp.Description("role in which workspace. require when level is workspace.")),
			mcp.WithString("project", mcp.Description("role in which project. require when level is namespace.")),
		)...),
			func(a args) (string, url.Values, error) {
				p, err := rolePath(a, "")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_role", `
Get role information of one level. The response will contain:
- roleName: Maps to metadata.name
- rules: the Kubernetes RBAC rules of the role.
`,
			mcp.WithString("level", mcp.Description("role level. it's four level: global (platform-level), cluster (cluster-level), workspace (workspace-level), namespace (project-level)"), mcp.Required()),
			mcp.WithString("cluster", mcp.Description("the given clusterName which role belong to. require when level is cluster or namespace.")),
			mcp.WithString("workspace", mcp.Description("role in which workspace. require when level is workspace.")),
			mcp.WithString("project", mcp.Description("role in which project. require when level is namespace.")),
			mcp.WithString("rolename", mcp.Description("the given rolename"), mcp.Required()),
		),
			func(a args) (string, url.Values, error) {
				name, err := a.need("rolename")
				if err != nil {
					return "", nil, err
				}
				p, err := rolePath(a, name)
				return p, nil, err
			}),
	}
}

// rolePath lists roles of the requested level, or gets one role when name is set.
func rolePath(a args, name string) (string, error) {
	var (
		cluster  string
		segments []string
	)
	switch level := a.str("level"); level {
	case constants.PlatformLevel:
		segments = []string{"globalroles"}
	case constants.ClusterLevel:
		cluster = a.str("cluster")
		segments = []string{"clusterroles"}
	case constants.WorkspaceLevel:
		ws, err := a.need("workspace")
		if err != nil {
			return "", err
		}
		segments = []string{"workspaces", ws, "workspaceroles"}
	case constants.ProjectLevel:
		project, err := a.need("project")
		if err != nil {
			return "", err
		}
		cluster = a.str("cluster")
		segments = []string{"namespaces", project, "roles"}
	default:
		return "", fmt.Errorf("level must be one of %s, %s, %s, %s; got %q",
			constants.PlatformLevel, constants.ClusterLevel, constants.WorkspaceLevel, constants.ProjectLevel, level)
	}
	if name != "" {
		segments = append(segments, name)
	}
	return kubesphere.KAPIPath(cluster, constants.IAMV1alpha2, segments...)
}
