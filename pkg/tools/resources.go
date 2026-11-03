package tools

import (
	"fmt"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

// projectResources are listed per project, or across all projects of a cluster.
var projectResources = []string{
	"deployments", "statefulsets", "daemonsets", "jobs", "cronjobs", "pods",
	"services", "ingresses", "secrets", "configmaps", "serviceaccounts", "persistentvolumeclaims",
}

// clusterResources are cluster-scoped.
var clusterResources = []string{"nodes", "customresourcedefinitions", "persistentvolumes", "storageclasses"}

func resourceTools(ks *kubesphere.KSConfig) []server.ServerTool {
	var tools []server.ServerTool
	for _, resource := range clusterResources {
		tools = append(tools, tool(ks, newTool("list_"+resource, fmt.Sprintf(`
Retrieve the paginated %[1]s list of a cluster. The response will include:
1. items: An array of %[1]s objects containing:
the item actual is %[1]s resource in kubernetes.
2. totalItems: The total number of %[1]s in the cluster.
A normal user may get 403 for cluster-scoped resources; that is RBAC, not an error in this tool.
`, resource), append(pagingOptions(resource), clusterOption(true))...),
			resourceRoute(resource, false)))
	}
	for _, resource := range projectResources {
		description := fmt.Sprintf(`
Retrieve the paginated %[1]s list. The response will include:
1. items: An array of %[1]s objects containing:
the item actual is %[1]s resource in kubernetes.
2. totalItems: The total number of %[1]s in KubeSphere.
`, resource)
		var post func([]byte) ([]byte, error)
		if resource == "secrets" {
			description += `Secret values are never returned: data and stringData are replaced by dataKeys, the sorted key names,
and the kubectl.kubernetes.io/last-applied-configuration annotation is removed.
`
			post = redactSecretList
		}
		t := newTool("list_"+resource, description, append(pagingOptions(resource),
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the given projectName, if empty will return all project "+resource)))...)
		tools = append(tools, server.ServerTool{Tool: t, Handler: handle(ks, resourceRoute(resource, true), post)})
	}
	return tools
}

func resourceRoute(resource string, namespaced bool) route {
	return func(a args) (string, url.Values, error) {
		cluster, err := a.need("cluster")
		if err != nil {
			return "", nil, err
		}
		segments := []string{resource}
		if project := a.str("project"); namespaced && project != "" {
			segments = []string{"namespaces", project, resource}
		}
		p, err := kubesphere.KAPIPath(cluster, constants.ResourcesV1alpha3, segments...)
		return p, a.paging("name"), err
	}
}
