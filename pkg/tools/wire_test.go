package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Clients saw empty annotations and only these four keys before the mcp-go v1
// upgrade; a library default must not change what tools/list advertises.
func TestToolWireShapeIsUnchanged(t *testing.T) {
	_, ks := newRecorder(t)
	all := All(ks)
	require.NotEmpty(t, all)
	for _, st := range all {
		raw, err := json.Marshal(st.Tool)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t, []string{"name", "description", "inputSchema", "annotations"}, keys, st.Tool.Name)
		assert.Equal(t, map[string]any{}, m["annotations"], st.Tool.Name)
	}
}
