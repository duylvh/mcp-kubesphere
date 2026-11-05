package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

func clusterTools(ks *kubesphere.KSConfig) []server.ServerTool {
	return []server.ServerTool{
		{Tool: newTool("list_clusters", "List clusters: {items, totalItems}, paged. metadata.name is the cluster name; label cluster-role.kubesphere.io/host marks the host cluster, and other labels are tags (see get_cluster_tags).", pagingOptions("clusters")...),
			Handler: handle(ks, func(a args) (string, url.Values, error) {
				p, err := kubesphere.KAPIPath("", constants.ResourcesV1alpha3, "clusters")
				return p, a.paging("createTime"), err
			}, stripClusterCredentials)},

		{Tool: newTool("get_cluster", "Get one cluster: metadata.name, role labels (cluster-role.kubesphere.io/host or /edge), status.kubernetesVersion and status.nodeCount.", mcp.WithString("cluster", mcp.Description("the given clusterName"), mcp.Required())),
			Handler: handle(ks, func(a args) (string, url.Values, error) {
				cluster, err := a.need("cluster")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.ResourcesV1alpha3, "clusters", cluster)
				return p, nil, err
			}, stripClusterCredentials)},

		{
			Tool:    newTool("get_cluster_tags", "Map each cluster label key to its values and the clusters carrying each."),
			Handler: clusterTags(ks),
		},

		tool(ks, newTool("list_cluster_members", "List a cluster's members: {items, totalItems}, paged. metadata.name is the username; annotation iam.kubesphere.io/clusterrole is the role.", append(pagingOptions("cluster members"), clusterOption(true))...),
			func(a args) (string, url.Values, error) {
				cluster, err := a.need("cluster")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath(cluster, constants.IAMV1alpha2, "clustermembers")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("list_projects", "List the projects (namespaces) of a cluster: {items, totalItems}, paged; label kubesphere.io/workspace names each project's workspace. With workspace, only that workspace's projects the signed-in user is a member of.", append(pagingOptions("projects"),
			clusterOption(true),
			mcp.WithString("workspace", mcp.Description("the given workspaceName")))...),
			func(a args) (string, url.Values, error) {
				cluster, err := a.need("cluster")
				if err != nil {
					return "", nil, err
				}
				if ws := a.str("workspace"); ws != "" {
					p, err := kubesphere.KAPIPath(cluster, constants.TenantV1alpha2, "workspaces", ws, "namespaces")
					return p, a.paging("createTime"), err
				}
				p, err := kubesphere.KAPIPath(cluster, constants.ResourcesV1alpha3, "namespaces")
				return p, a.paging("name"), err
			}),
	}
}

// clusterTags groups every cluster by its labels. KubeSphere v3.3 has no
// cluster label API, so the map is derived from the cluster list.
func clusterTags(ks *kubesphere.KSConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		p, err := kubesphere.KAPIPath("", constants.ResourcesV1alpha3, "clusters")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, err := ks.Get(ctx, p, nil)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name   string            `json:"name"`
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("unexpected cluster list from KubeSphere: %v", err)), nil
		}
		tags := map[string]map[string][]string{}
		for _, item := range list.Items {
			for key, value := range item.Metadata.Labels {
				if tags[key] == nil {
					tags[key] = map[string][]string{}
				}
				tags[key][value] = append(tags[key][value], item.Metadata.Name)
			}
		}
		for _, values := range tags {
			for _, clusters := range values {
				sort.Strings(clusters)
			}
		}
		out, err := json.Marshal(tags)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(out)), nil
	}
}

// stripClusterCredentials removes spec.connection.token and
// spec.connection.kubeconfig from a cluster or a cluster list. KubeSphere
// v3.3 clears only kubeconfig, and token is the agent credential of a proxy
// cluster.
func stripClusterCredentials(body []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("KubeSphere returned an unexpected cluster response; it is withheld because it may contain credentials")
	}
	strip := func(cluster any) {
		c, _ := cluster.(map[string]any)
		spec, _ := c["spec"].(map[string]any)
		conn, _ := spec["connection"].(map[string]any)
		delete(conn, "token")
		delete(conn, "kubeconfig")
	}
	if items, ok := doc["items"].([]any); ok {
		for _, item := range items {
			strip(item)
		}
	} else {
		strip(doc)
	}
	return json.Marshal(doc)
}
