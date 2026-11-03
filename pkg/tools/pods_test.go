package tools

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPodLogArguments(t *testing.T) {
	rec, ks := newRecorder(t)
	tools := byName(ks)

	call(t, tools, "get_pod_logs", map[string]any{
		"cluster": "prod-3", "project": "p1", "pod": "svc-0", "container": "app",
		"tailLines": float64(500), "sinceSeconds": float64(600), "previous": true, "timestamps": "true",
	})
	assert.Equal(t, "/api/clusters/prod-3/v1/namespaces/p1/pods/svc-0/log?container=app&previous=true&sinceSeconds=600&tailLines=500&timestamps=true", rec.last())

	call(t, tools, "get_pod_logs", map[string]any{"cluster": "dev", "project": "p1", "pod": "svc-0", "tailLines": float64(1e6), "previous": false})
	assert.Equal(t, "/api/clusters/dev/v1/namespaces/p1/pods/svc-0/log?tailLines=2000", rec.last(), "tailLines is capped and false flags are omitted")

	before := len(rec.uris)
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"get_pod", map[string]any{"cluster": "dev", "project": "p1"}, `argument "pod" is required`},
		{"get_pod", map[string]any{"cluster": "dev", "pod": "svc-0"}, `argument "project" is required`},
		{"get_pod", map[string]any{"cluster": "dev", "project": "p1", "pod": ".."}, "invalid name"},
		{"get_pod_logs", map[string]any{"project": "p1", "pod": "svc-0"}, `argument "cluster" is required`},
		{"get_pod_logs", map[string]any{"cluster": "dev", "pod": "svc-0"}, `argument "project" is required`},
		{"get_pod_logs", map[string]any{"cluster": "dev", "project": "p1"}, `argument "pod" is required`},
		{"get_pod_logs", map[string]any{"cluster": "dev", "project": "p1", "pod": "../../secrets/x"}, "invalid name"},
		{"list_events", map[string]any{"project": "p1"}, `argument "cluster" is required`},
		{"list_events", map[string]any{"cluster": "dev"}, `argument "project" is required`},
		{"list_events", map[string]any{"cluster": "dev", "project": "p1", "name": "svc,type=Warning"}, "invalid name"},
		{"list_events", map[string]any{"cluster": "dev", "project": "p1", "kind": "Pod!=x"}, "invalid kind"},
	} {
		res := call(t, tools, tc.tool, tc.args)
		assert.True(t, res.IsError, "%s %v", tc.tool, tc.args)
		assert.Contains(t, text(res), tc.want)
	}
	assert.Len(t, rec.uris, before, "invalid arguments never reach KubeSphere")
}

func TestPodLogIsTextAndKeepsNewestLines(t *testing.T) {
	rec, ks := newRecorder(t)
	tools := byName(ks)
	logPath := "/api/clusters/dev/v1/namespaces/p1/pods/svc-0/log"
	args := map[string]any{"cluster": "dev", "project": "p1", "pod": "svc-0"}

	rec.bodies[logPath] = "line 1\nline 2\n"
	res := call(t, tools, "get_pod_logs", args)
	require.False(t, res.IsError, text(res))
	assert.Equal(t, "line 1\nline 2\n", text(res), "a log is returned verbatim, not as JSON")

	var big strings.Builder
	for i := 0; big.Len() <= maxLogBytes+1000; i++ {
		big.WriteString(strings.Repeat("x", 99) + "\n")
	}
	big.WriteString("NEWEST\n")
	rec.bodies[logPath] = big.String()
	out := text(call(t, tools, "get_pod_logs", args))
	assert.True(t, strings.HasPrefix(out, "[log trimmed: "), out[:80])
	assert.True(t, strings.HasSuffix(out, "NEWEST\n"), "the newest line survives trimming")
	firstLine := strings.SplitN(out, "\n", 3)[1]
	assert.Equal(t, strings.Repeat("x", 99), firstLine, "trimming cuts at a line start")
	assert.LessOrEqual(t, len(out), maxLogBytes+100)
}

func TestListEventsNewestFirst(t *testing.T) {
	rec, ks := newRecorder(t)
	rec.bodies["/api/clusters/dev/v1/namespaces/p1/events"] = `{"kind":"EventList","items":[
		{"metadata":{"name":"a","managedFields":[{"manager":"m"}]},"reason":"Scheduled","lastTimestamp":null,"eventTime":"2026-11-03T15:45:05.123456Z"},
		{"metadata":{"name":"b"},"reason":"Pulled","lastTimestamp":"2026-11-03T15:45:11Z"},
		{"metadata":{"name":"c"},"reason":"BackOff","lastTimestamp":"2026-11-03T15:50:00Z"},
		{"metadata":{"name":"d","creationTimestamp":"2026-11-03T15:40:00Z"},"reason":"Old"}]}`
	res := call(t, byName(ks), "list_events", map[string]any{"cluster": "dev", "project": "p1", "name": "svc-0", "kind": "Pod", "limit": float64(3)})
	require.False(t, res.IsError, text(res))
	assert.Equal(t, "/api/clusters/dev/v1/namespaces/p1/events?fieldSelector=involvedObject.name%3Dsvc-0%2CinvolvedObject.kind%3DPod", rec.last())
	assert.JSONEq(t, `{"items":[
		{"metadata":{"name":"c"},"reason":"BackOff","lastTimestamp":"2026-11-03T15:50:00Z"},
		{"metadata":{"name":"b"},"reason":"Pulled","lastTimestamp":"2026-11-03T15:45:11Z"},
		{"metadata":{"name":"a"},"reason":"Scheduled","lastTimestamp":null,"eventTime":"2026-11-03T15:45:05.123456Z"}],
		"totalItems":4}`, text(res))
}

func TestGetPodKeepsStatusAndDropsNoise(t *testing.T) {
	rec, ks := newRecorder(t)
	rec.bodies["/kapis/clusters/dev/resources.kubesphere.io/v1alpha3/namespaces/p1/pods/svc-0"] = `{"metadata":{"name":"svc-0","managedFields":[{"manager":"m"}],"annotations":{"kubectl.kubernetes.io/last-applied-configuration":"{}","keep":"yes"}},
		"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"exitCode":137,"reason":"OOMKilled"}}}]}}`
	res := call(t, byName(ks), "get_pod", map[string]any{"cluster": "dev", "project": "p1", "pod": "svc-0"})
	require.False(t, res.IsError, text(res))
	assert.JSONEq(t, `{"metadata":{"name":"svc-0","annotations":{"keep":"yes"}},
		"status":{"phase":"Running","containerStatuses":[{"name":"app","ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}},"lastState":{"terminated":{"exitCode":137,"reason":"OOMKilled"}}}]}}`, text(res))
}
