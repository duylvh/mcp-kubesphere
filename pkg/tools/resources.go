package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

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
		tools = append(tools, filteredTool(ks, newTool("list_"+resource, fmt.Sprintf(
			"List the %s of a cluster as Kubernetes objects: {items, totalItems}, paged. Cluster-scoped, so a normal user may get 403.", resource), append(append(pagingOptions(resource), filterOptions(resource)...), clusterOption(true))...),
			resourceRoute(resource, false), compactList))
	}
	for _, resource := range projectResources {
		description := fmt.Sprintf(
			"List %s as Kubernetes objects: {items, totalItems}, paged. In a busy project, narrow with name or labelSelector.", resource)
		post := compactList
		if resource == "secrets" {
			description += " Values are never returned: data and stringData become dataKeys, the sorted key names."
			post = redactSecretList
		}
		t := newTool("list_"+resource, description, append(append(pagingOptions(resource), filterOptions(resource)...),
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the project (namespace). Omitted, every project is listed, which needs cluster-wide permission")))...)
		tools = append(tools, server.ServerTool{Tool: t, Handler: projectHint(handle(ks, resourceRoute(resource, true), post))})
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

// projectHint turns a 403 on a cluster-wide list into the call that usually
// works: most users may read only the projects they belong to.
func projectHint(h server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := h(ctx, request)
		if err != nil || res == nil || !res.IsError || args(request.Params.Arguments).str("project") != "" {
			return res, err
		}
		for _, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok && strings.Contains(tc.Text, "returned 403") {
				return mcp.NewToolResultError(tc.Text + "; pass project to list one project you can read"), nil
			}
		}
		return res, err
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
		compactItem(item)
	}
	compacted, err := json.Marshal(items)
	if err != nil {
		return body, nil
	}
	list["items"] = compacted
	return json.Marshal(list)
}

func compactItem(item map[string]any) {
	metadata, _ := item["metadata"].(map[string]any)
	delete(metadata, "managedFields")
	annotations, _ := metadata["annotations"].(map[string]any)
	delete(annotations, lastAppliedAnnotation)
}

// compactObject applies compactList's trimming to a single object.
func compactObject(body []byte) ([]byte, error) {
	var item map[string]any
	if json.Unmarshal(body, &item) != nil {
		return body, nil
	}
	compactItem(item)
	return json.Marshal(item)
}

// filterOptions narrow a list on the server, so a busy namespace does not
// return every object: name matches a substring of metadata.name, and
// labelSelector takes Kubernetes selector syntax.
func filterOptions(resource string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("name", mcp.Description("only names containing this text")),
		mcp.WithString("labelSelector", mcp.Description("Kubernetes label selector, e.g. app=my-service")),
	}
}
