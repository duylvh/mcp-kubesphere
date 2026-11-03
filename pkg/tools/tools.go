// Package tools defines the MCP tools that read KubeSphere v3.3 through the
// /kapis API.
package tools

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

// All returns every tool, in the order clients list them.
func All(ks *kubesphere.KSConfig) []server.ServerTool {
	var tools []server.ServerTool
	tools = append(tools, iamTools(ks)...)
	tools = append(tools, workspaceTools(ks)...)
	tools = append(tools, applicationTools(ks)...)
	tools = append(tools, clusterTools(ks)...)
	tools = append(tools, resourceTools(ks)...)
	tools = append(tools, podTools(ks)...)
	return tools
}

// args reads tool arguments, which arrive as decoded JSON.
type args map[string]any

func (a args) str(name string) string {
	s, _ := a[name].(string)
	return s
}

func (a args) need(name string) (string, error) {
	s := a.str(name)
	if s == "" {
		return "", fmt.Errorf("argument %q is required", name)
	}
	return s, nil
}

// boolean reads a JSON boolean, or the string "true".
func (a args) boolean(name string) bool {
	switch v := a[name].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// positive returns a whole number argument, or def when it is absent or not a
// positive whole number. JSON numbers arrive as float64.
func (a args) positive(name string, def int) int {
	switch v := a[name].(type) {
	case float64:
		if v >= 1 && v == math.Trunc(v) && v <= math.MaxInt32 {
			return int(v)
		}
	case int:
		if v >= 1 {
			return v
		}
	case int64:
		if v >= 1 && v <= math.MaxInt32 {
			return int(v)
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return n
		}
	}
	return def
}

// paging builds the KubeSphere list query: sortBy, limit and page.
func (a args) paging(sortBy string) url.Values {
	return url.Values{
		"sortBy": {sortBy},
		"limit":  {strconv.Itoa(a.positive("limit", constants.DefLimit))},
		"page":   {strconv.Itoa(a.positive("page", constants.DefPage))},
	}
}

// route resolves the request for one call; a returned error is shown to the
// caller as a tool error.
type route func(a args) (path string, query url.Values, err error)

// handle sends the routed GET request and returns the body as text.
// Failures become tool errors so the model sees the KubeSphere message.
func handle(ks *kubesphere.KSConfig, r route, post func([]byte) ([]byte, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, query, err := r(args(request.Params.Arguments))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		body, err := ks.Get(ctx, path, query)
		if err == nil && post != nil {
			body, err = post(body)
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(string(body)), nil
	}
}

func tool(ks *kubesphere.KSConfig, t mcp.Tool, r route) server.ServerTool {
	return server.ServerTool{Tool: t, Handler: handle(ks, r, nil)}
}

func pagingOptions(noun string) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Number of %s displayed at once. Default is %d", noun, constants.DefLimit))),
		mcp.WithNumber("page", mcp.Description(fmt.Sprintf("Page number of %s to display. Default is %d", noun, constants.DefPage))),
	}
}

const clusterHelp = "the given clusterName, e.g. host or a member cluster. Use list_clusters to find names; a workspace's projects live on the clusters in get_workspace spec.placement.clusters"

func clusterOption(required bool) mcp.ToolOption {
	if required {
		return mcp.WithString("cluster", mcp.Description(clusterHelp), mcp.Required())
	}
	return mcp.WithString("cluster", mcp.Description(clusterHelp))
}

func newTool(name, description string, opts ...mcp.ToolOption) mcp.Tool {
	return mcp.NewTool(name, append([]mcp.ToolOption{mcp.WithDescription(description)}, opts...)...)
}
