package tools

import (
	"fmt"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

// applicationTools read the OpenPitrix app store and the Helm applications
// installed from it.
func applicationTools(ks *kubesphere.KSConfig) []server.ServerTool {
	return []server.ServerTool{
		tool(ks, newTool("list_application_repos", "List a workspace's Helm app repositories: {items (repo_id, name, url), total_count}, paged.", append(pagingOptions("application repositories"),
			mcp.WithString("workspaceName", mcp.Description("the given workspaceName"), mcp.Required()))...),
			func(a args) (string, url.Values, error) {
				ws, err := a.need("workspaceName")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.OpenPitrixV1, "workspaces", ws, "repos")
				return p, openPitrixPaging(a), err
			}),

		tool(ks, newTool("list_applications", "List the apps installed from app templates in a project: {items, total_count}, paged. cluster.cluster_id is the id for get_application; app.app_id is the id for get_application_version.", append(pagingOptions("applications"),
			mcp.WithString("workspace", mcp.Description("the given workspaceName which the project belongs to"), mcp.Required()),
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the given projectName"), mcp.Required()))...),
			func(a args) (string, url.Values, error) {
				p, err := applicationPath(a, "")
				return p, openPitrixPaging(a), err
			}),

		tool(ks, newTool("get_application", "Get an app installed from an app template: cluster (id, name, status, env), the installed version, and the app template.",
			mcp.WithString("workspace", mcp.Description("the given workspaceName which the project belongs to"), mcp.Required()),
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the given projectName"), mcp.Required()),
			mcp.WithString("applicationName", mcp.Description("the application id (cluster.cluster_id from list_applications)"), mcp.Required()),
		),
			func(a args) (string, url.Values, error) {
				id, err := a.need("applicationName")
				if err != nil {
					return "", nil, err
				}
				p, err := applicationPath(a, id)
				return p, nil, err
			}),

		tool(ks, newTool("get_application_version", "List the versions of an app template: {items (version_id, name, review status), total_count}, paged.", append(pagingOptions("application versions"),
			mcp.WithString("applicationName", mcp.Description("the app template id (app.app_id), e.g. app-xxxx"), mcp.Required()))...),
			func(a args) (string, url.Values, error) {
				app, err := a.need("applicationName")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.OpenPitrixV1, "apps", app, "versions")
				return p, openPitrixPaging(a), err
			}),
	}
}

// applicationPath addresses the applications of a project, or one of them.
func applicationPath(a args, id string) (string, error) {
	ws, err := a.need("workspace")
	if err != nil {
		return "", err
	}
	cluster, err := a.need("cluster")
	if err != nil {
		return "", err
	}
	project, err := a.need("project")
	if err != nil {
		return "", err
	}
	segments := []string{"workspaces", ws, "clusters", cluster, "namespaces", project, "applications"}
	if id != "" {
		segments = append(segments, id)
	}
	return kubesphere.KAPIPath("", constants.OpenPitrixV1, segments...)
}

// openPitrixPaging uses the OpenPitrix list parameters: one "paging" value
// ("limit=10,page=1") and orderBy, newest first.
func openPitrixPaging(a args) url.Values {
	return url.Values{
		"paging":  {fmt.Sprintf("limit=%d,page=%d", a.positive("limit", constants.DefLimit), a.positive("page", constants.DefPage))},
		"orderBy": {"createTime"},
		"reverse": {"true"},
	}
}
