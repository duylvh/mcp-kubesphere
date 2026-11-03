package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/duylvh/mcp-kubesphere/pkg/constants"
	"github.com/duylvh/mcp-kubesphere/pkg/kubesphere"
)

const (
	defLogLines   = 100
	maxLogLines   = 2000
	maxLogBytes   = 64 << 10
	defEventLimit = 20
	maxEventLimit = 200
)

// podTools read one pod, and what the /kapis resource lists do not carry:
// container logs and events, which go through the Kubernetes core API that
// ks-apiserver proxies per cluster.
func podTools(ks *kubesphere.KSConfig) []server.ServerTool {
	return []server.ServerTool{
		{Tool: newTool("get_pod", `
Get one pod with its full spec and status. status holds:
- phase, conditions (PodScheduled, Initialized, ContainersReady, Ready) with reason and message.
- containerStatuses and initContainerStatuses: per container ready, restartCount, image, and
  state / lastState (waiting with reason such as CrashLoopBackOff or ImagePullBackOff,
  running with startedAt, terminated with exitCode, reason such as OOMKilled or Error, and finishedAt).
- hostIP, podIP, startTime, qosClass.
Find the pod with list_pods. The object omits metadata.managedFields and the
kubectl.kubernetes.io/last-applied-configuration annotation; container env values are included.
`,
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the project (namespace) of the pod"), mcp.Required()),
			mcp.WithString("pod", mcp.Description("the pod name, from list_pods"), mcp.Required()),
		), Handler: handle(ks, podRoute, compactObject)},

		{Tool: newTool("get_pod_logs", fmt.Sprintf(`
Read the log of one container of a pod, as plain text, newest lines last.
- Find the pod with list_pods (name or labelSelector app=<deployment>).
- A pod with more than one container needs container; the error lists the names.
- previous=true reads the container's last terminated run, the one to read for a crash loop.
- Returns the last tailLines lines (default %[1]d, at most %[2]d). Output over %[3]d KiB keeps its newest lines and starts with a note that older lines were cut.
Log lines are returned as written by the application and may contain sensitive data.
A 403 means the signed-in user lacks permission to read pod logs in that project.
`, defLogLines, maxLogLines, maxLogBytes>>10),
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the project (namespace) of the pod"), mcp.Required()),
			mcp.WithString("pod", mcp.Description("the pod name, from list_pods"), mcp.Required()),
			mcp.WithString("container", mcp.Description("the container name; required when the pod has more than one container")),
			mcp.WithNumber("tailLines", mcp.Description(fmt.Sprintf("number of most recent lines to return. Default %d, at most %d", defLogLines, maxLogLines))),
			mcp.WithNumber("sinceSeconds", mcp.Description("only lines written in the last this many seconds")),
			mcp.WithBoolean("previous", mcp.Description("read the previous terminated run of the container, e.g. before a restart")),
			mcp.WithBoolean("timestamps", mcp.Description("prefix each line with the time Kubernetes received it")),
		), Handler: handle(ks, podLogsRoute, trimLog)},

		{Tool: newTool("list_events", fmt.Sprintf(`
Retrieve the Kubernetes events of a project, newest first: scheduling, image pulls, probe failures,
OOM kills, back-off restarts and scaling. The response will include:
1. items: up to limit (at most %[1]d) event objects (type, reason, message, count, involvedObject, firstTimestamp, lastTimestamp).
2. totalItems: the number of events that matched, before limit.
Kubernetes keeps events for about one hour. If a project has too many events, filter by name or kind.
`, maxEventLimit),
			clusterOption(true),
			mcp.WithString("project", mcp.Description("the project (namespace)"), mcp.Required()),
			mcp.WithString("name", mcp.Description("only events about the object with exactly this name, e.g. a pod name")),
			mcp.WithString("kind", mcp.Description("only events about objects of this kind, e.g. Pod, Deployment, ReplicaSet, HorizontalPodAutoscaler")),
			mcp.WithNumber("limit", mcp.Description(fmt.Sprintf("Number of events returned. Default is %d, at most %d", defEventLimit, maxEventLimit))),
		), Handler: listEvents(ks)},
	}
}

func podRoute(a args) (string, url.Values, error) {
	cluster, err := a.need("cluster")
	if err != nil {
		return "", nil, err
	}
	project, err := a.need("project")
	if err != nil {
		return "", nil, err
	}
	pod, err := a.need("pod")
	if err != nil {
		return "", nil, err
	}
	p, err := kubesphere.KAPIPath(cluster, constants.ResourcesV1alpha3, "namespaces", project, "pods", pod)
	return p, nil, err
}

func podLogsRoute(a args) (string, url.Values, error) {
	cluster, err := a.need("cluster")
	if err != nil {
		return "", nil, err
	}
	project, err := a.need("project")
	if err != nil {
		return "", nil, err
	}
	pod, err := a.need("pod")
	if err != nil {
		return "", nil, err
	}
	p, err := kubesphere.CoreV1Path(cluster, "namespaces", project, "pods", pod, "log")
	if err != nil {
		return "", nil, err
	}
	q := url.Values{"tailLines": {strconv.Itoa(min(a.positive("tailLines", defLogLines), maxLogLines))}}
	if c := a.str("container"); c != "" {
		q.Set("container", c)
	}
	if s := a.positive("sinceSeconds", 0); s > 0 {
		q.Set("sinceSeconds", strconv.Itoa(s))
	}
	for _, flag := range []string{"previous", "timestamps"} {
		if a.boolean(flag) {
			q.Set(flag, "true")
		}
	}
	return p, q, nil
}

// trimLog keeps the newest maxLogBytes of a log, so the lines a caller is
// most likely after survive. It cuts at the first line start in the newest
// maxLogBytes, which may be the window's first byte. When the kept part holds
// no complete line, it cuts at the next UTF-8 character start instead, looking
// at most utf8.UTFMax-1 bytes ahead, so a valid character is never split and
// output that is not UTF-8 is still returned rather than skipped.
func trimLog(body []byte) ([]byte, error) {
	if len(body) <= maxLogBytes {
		return body, nil
	}
	cut := len(body) - maxLogBytes
	// A cut right after a line break already starts a line; searching on
	// would drop that line although it fits.
	atLine := body[cut-1] == '\n'
	if i := bytes.IndexByte(body[cut:], '\n'); !atLine && i >= 0 && cut+i+1 < len(body) {
		cut += i + 1
		atLine = true
	}
	for step := 0; !atLine && step < utf8.UTFMax-1 && cut < len(body) && !utf8.RuneStart(body[cut]); step++ {
		cut++
	}
	advice := "lower tailLines or use sinceSeconds"
	if !atLine {
		advice = fmt.Sprintf("the newest line alone exceeds %d KiB and starts partway through", maxLogBytes>>10)
	}
	note := fmt.Sprintf("[log trimmed: %d older bytes omitted; %s]\n", cut, advice)
	return append([]byte(note), body[cut:]...), nil
}

// eventsRoute fetches the whole event list, narrowed only by fieldSelector:
// Kubernetes returns events in name order, so its own limit would keep an
// arbitrary subset. listEvents sorts and limits the result instead.
func eventsRoute(a args) (string, url.Values, error) {
	cluster, err := a.need("cluster")
	if err != nil {
		return "", nil, err
	}
	project, err := a.need("project")
	if err != nil {
		return "", nil, err
	}
	p, err := kubesphere.CoreV1Path(cluster, "namespaces", project, "events")
	if err != nil {
		return "", nil, err
	}
	var selectors []string
	for _, f := range []struct{ arg, field string }{{"name", "involvedObject.name"}, {"kind", "involvedObject.kind"}} {
		v := a.str(f.arg)
		if v == "" {
			continue
		}
		if strings.ContainsAny(v, ",=!\\ ") {
			return "", nil, fmt.Errorf("invalid %s %q", f.arg, v)
		}
		selectors = append(selectors, f.field+"="+v)
	}
	q := url.Values{}
	if len(selectors) > 0 {
		q.Set("fieldSelector", strings.Join(selectors, ","))
	}
	return p, q, nil
}

func listEvents(ks *kubesphere.KSConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a := args(request.Params.Arguments)
		limit := min(a.positive("limit", defEventLimit), maxEventLimit)
		return handle(ks, eventsRoute, func(body []byte) ([]byte, error) { return newestEvents(body, limit) })(ctx, request)
	}
}

type event struct {
	raw      map[string]any
	happened time.Time
}

func newestEvents(body []byte, limit int) ([]byte, error) {
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decoding KubeSphere event list: %w", err)
	}
	events := make([]event, len(list.Items))
	for i, item := range list.Items {
		events[i] = event{raw: item, happened: eventTime(item)}
		if metadata, ok := item["metadata"].(map[string]any); ok {
			delete(metadata, "managedFields")
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].happened.After(events[j].happened) })
	items := make([]map[string]any, 0, min(limit, len(events)))
	for _, e := range events[:min(limit, len(events))] {
		items = append(items, e.raw)
	}
	return json.Marshal(map[string]any{"items": items, "totalItems": len(events)})
}

// eventTime is when an event last happened: lastTimestamp, or for events
// recorded through events.k8s.io, eventTime (series.lastObservedTime when the
// event repeats), then firstTimestamp, then the object's creation time.
func eventTime(item map[string]any) time.Time {
	series, _ := item["series"].(map[string]any)
	metadata, _ := item["metadata"].(map[string]any)
	for _, v := range []any{item["lastTimestamp"], series["lastObservedTime"], item["eventTime"], item["firstTimestamp"], metadata["creationTimestamp"]} {
		if s, ok := v.(string); ok && s != "" {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}
