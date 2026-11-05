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
		tool(ks, newTool("list_users", "List KubeSphere users: {items, totalItems}, paged. metadata.name is the username, spec.email the email; annotations iam.kubesphere.io/globalrole (platform role) and iam.kubesphere.io/last-login-time.", pagingOptions("users")...),
			func(a args) (string, url.Values, error) {
				p, err := kubesphere.KAPIPath("", constants.IAMV1alpha2, "users")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_user", "Get one user: metadata.name, spec.email, and the platform role in annotation iam.kubesphere.io/globalrole.", mcp.WithString("user", mcp.Description("the given username"), mcp.Required())),
			func(a args) (string, url.Values, error) {
				user, err := a.need("user")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.IAMV1alpha2, "users", user)
				return p, nil, err
			}),

		tool(ks, newTool("list_roles", "List the roles of one level: {items, totalItems}, paged. Each has metadata.name, its RBAC rules, and the role templates it aggregates in annotation iam.kubesphere.io/aggregation-roles. A normal user may get 403 for platform or workspace roles.", append(pagingOptions("roles"),
			mcp.WithString("level", mcp.Description("global (platform), cluster, workspace, or namespace (project)"), mcp.Required()),
			mcp.WithString("cluster", mcp.Description("required for level cluster or namespace")),
			mcp.WithString("workspace", mcp.Description("required for level workspace")),
			mcp.WithString("project", mcp.Description("required for level namespace")),
		)...),
			func(a args) (string, url.Values, error) {
				p, err := rolePath(a, "")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("get_role", "Get one role of a level: metadata.name and its RBAC rules.",
			mcp.WithString("level", mcp.Description("global (platform), cluster, workspace, or namespace (project)"), mcp.Required()),
			mcp.WithString("cluster", mcp.Description("required for level cluster or namespace")),
			mcp.WithString("workspace", mcp.Description("required for level workspace")),
			mcp.WithString("project", mcp.Description("required for level namespace")),
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
