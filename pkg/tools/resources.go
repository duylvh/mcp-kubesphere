package tools

import (
	"encoding/json"
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
		tools = append(tools, filteredTool(ks, newTool("list_"+resource, fmt.Sprintf(`
Retrieve the paginated %[1]s list of a cluster. The response will include:
1. items: An array of %[1]s objects containing:
the item actual is %[1]s resource in kubernetes.
2. totalItems: The total number of %[1]s in the cluster.
A normal user may get 403 for cluster-scoped resources; that is RBAC, not an error in this tool.
`, resource), append(append(pagingOptions(resource), filterOptions(resource)...), clusterOption(true))...),
			resourceRoute(resource, false), compactList))
	}
	for _, resource := range projectResources {
		description := fmt.Sprintf(`
Retrieve the paginated %[1]s list. The response will include:
1. items: An array of %[1]s objects containing:
the item actual is %[1]s resource in kubernetes.
2. totalItems: The total number of %[1]s in KubeSphere.
`, resource)
		post := compactList
		if resource == "secrets" {
			description += `Secret values are never returned: data and stringData are replaced by dataKeys, the sorted key names,
and the kubectl.kubernetes.io/last-applied-configuration annotation is removed.
`
			post = redactSecretList
		}
		t := newTool("list_"+resource, description, append(append(pagingOptions(resource), filterOptions(resource)...),
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
		q := a.paging("name")
		for _, filter := range []string{"name", "labelSelector"} {
			if v := a.str(filter); v != "" {
				q.Set(filter, v)
			}
		}
		return p, q, err
	}
}

func filteredTool(ks *kubesphere.KSConfig, t mcp.Tool, r route, post func([]byte) ([]byte, error)) server.ServerTool {
	return server.ServerTool{Tool: t, Handler: handle(ks, r, post)}
}

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// compactList drops metadata.managedFields and the
// kubectl.kubernetes.io/last-applied-configuration annotation from every item:
// both repeat the object (the annotation includes container env values) and
// make up most of a list's size. A body of another shape is returned as is.
func compactList(body []byte) ([]byte, error) {
	var list map[string]json.RawMessage
	if json.Unmarshal(body, &list) != nil {
		return body, nil
	}
	var items []map[string]any
	if raw, ok := list["items"]; !ok || json.Unmarshal(raw, &items) != nil {
		return body, nil
	}
	for _, item := range items {
		metadata, _ := item["metadata"].(map[string]any)
		delete(metadata, "managedFields")
		annotations, _ := metadata["annotations"].(map[string]any)
		delete(annotations, lastAppliedAnnotation)
	}
	compacted, err := json.Marshal(items)
	if err != nil {
		return body, nil
	}
	list["items"] = compacted
	return json.Marshal(list)
}

// filterOptions narrow a list on the server, so a busy namespace does not
// return every object: name matches a substring of metadata.name, and
// labelSelector takes Kubernetes selector syntax.
func filterOptions(resource string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("name", mcp.Description("only "+resource+" whose name contains this text, e.g. my-service")),
		mcp.WithString("labelSelector", mcp.Description("only "+resource+" matching this Kubernetes label selector, e.g. app=my-service")),
	}
}
