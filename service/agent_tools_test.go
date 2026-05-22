package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentToolsSchema_HasBothTools(t *testing.T) {
	tools := &AgentTools{}
	b, err := json.Marshal(tools.ToolsSchema())
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	arr := got["tools"].([]any)
	require.Len(t, arr, 2)
	names := []string{}
	for _, t := range arr {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	require.ElementsMatch(t, []string{"nimoos_search", "read_file_chunk"}, names)
}

func TestAgentTools_FiltersSchemaIsObject(t *testing.T) {
	tools := &AgentTools{}
	fs := tools.FiltersSchema()
	require.Contains(t, fs, "root_ids")
	require.Contains(t, fs, "mime_prefix")
}
