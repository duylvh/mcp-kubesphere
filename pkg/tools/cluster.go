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
		{Tool: newTool("list_clusters", `
Retrieve the paginated clusters list. The response will include:
1. items: An array of clusters objects containing:
  - clusterName: Maps to metadata.name
  - specific metadata.labels fields indicate:
   - cluster-role.kubesphere.io/host: the cluster role is host.
   - cluster-role.kubesphere.io/edge: the cluster is edge cluster which deploy in edge node.
   - other labels are the tags of the cluster (see get_cluster_tags).
2. totalItems: The total number of clusters in KubeSphere.
`, pagingOptions("clusters")...),
			Handler: handle(ks, func(a args) (string, url.Values, error) {
				p, err := kubesphere.KAPIPath("", constants.ResourcesV1alpha3, "clusters")
				return p, a.paging("createTime"), err
			}, stripClusterCredentials)},

		{Tool: newTool("get_cluster", `
Get cluster information. The response will contain:
- clusterName: Maps metadata.name
- specific metadata.labels fields indicate:
 - cluster-role.kubesphere.io/host: the cluster role is host.
 - cluster-role.kubesphere.io/edge: the cluster is edge cluster which deploy in edge node.
- status.kubernetesVersion and status.nodeCount describe the cluster.
`, mcp.WithString("cluster", mcp.Description("the given clusterName"), mcp.Required())),
			Handler: handle(ks, func(a args) (string, url.Values, error) {
				cluster, err := a.need("cluster")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath("", constants.ResourcesV1alpha3, "clusters", cluster)
				return p, nil, err
			}, stripClusterCredentials)},

		{
			Tool: newTool("get_cluster_tags", `
Retrieve the cluster tags map, built from the labels of every cluster. The response will include:
- map key is label key
- map value is a map from each label value to the clusters carrying it.
`),
			Handler: clusterTags(ks),
		},

		tool(ks, newTool("list_cluster_members", `
Retrieve the paginated cluster members list. The response will include:
1. items: An array of cluster member objects containing:
  - username: Maps to metadata.name
  - specific metadata.annotations fields indicate:
   - iam.kubesphere.io/clusterrole: the cluster role which this user belong to.
2. totalItems: The total number of cluster members in KubeSphere.
`, append(pagingOptions("cluster members"), clusterOption(true))...),
			func(a args) (string, url.Values, error) {
				cluster, err := a.need("cluster")
				if err != nil {
					return "", nil, err
				}
				p, err := kubesphere.KAPIPath(cluster, constants.IAMV1alpha2, "clustermembers")
				return p, a.paging("createTime"), err
			}),

		tool(ks, newTool("list_projects", `
Get project from cluster or workspace.
when Get from cluster. should set cluster param.
when Get from workspace. should set workspace and cluster which has assign to this workspace.
Retrieve the paginated projects list. The response will include:
1. items: An array of projects objects containing:
the item actual is namespace resource in kubernetes.
  - specific metadata.labels fields indicate:
   - kubesphere.io/workspace: the workspace the project belongs to.
2. totalItems: The total number of projects in KubeSphere.
`, append(pagingOptions("projects"),
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
