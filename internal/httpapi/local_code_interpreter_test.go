package httpapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildLocalCodeInterpreterAssistantTextAnnotationsPrefersInlineMentions(t *testing.T) {
	finalText, annotations := buildLocalCodeInterpreterAssistantTextAnnotations(
		"Created report.txt and plot.png.",
		"cntr_test",
		[]localCodeInterpreterGeneratedFile{
			{FileID: "cfile_report", Filename: "report.txt"},
			{FileID: "cfile_plot", Filename: "plot.png"},
		},
	)

	require.Equal(t, "Created report.txt and plot.png.", finalText)
	require.Len(t, annotations, 2)

	first, ok := annotations[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "container_file_citation", first["type"])
	require.Equal(t, "cfile_report", first["file_id"])
	require.EqualValues(t, 8, first["start_index"])
	require.EqualValues(t, 18, first["end_index"])

	second, ok := annotations[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "cfile_plot", second["file_id"])
	require.EqualValues(t, 23, second["start_index"])
	require.EqualValues(t, 31, second["end_index"])
}

func TestBuildLocalCodeInterpreterAssistantTextAnnotationsAppendsFallbackForMissingMentions(t *testing.T) {
	finalText, annotations := buildLocalCodeInterpreterAssistantTextAnnotations(
		"Done.",
		"cntr_test",
		[]localCodeInterpreterGeneratedFile{
			{FileID: "cfile_report", Filename: "report.txt"},
		},
	)

	require.Equal(t, "Done.\n\nGenerated files:\n- report.txt", finalText)
	require.Len(t, annotations, 1)
	expectedStart := strings.Index(finalText, "report.txt")
	expectedEnd := expectedStart + len("report.txt")

	annotation, ok := annotations[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "container_file_citation", annotation["type"])
	require.Equal(t, "cfile_report", annotation["file_id"])
	require.EqualValues(t, expectedStart, annotation["start_index"])
	require.EqualValues(t, expectedEnd, annotation["end_index"])
}

func TestBuildLocalCodeInterpreterCallItemWithStatusKeepsOutputsLogsOnly(t *testing.T) {
	item, err := buildLocalCodeInterpreterCallItemWithStatus(
		"print('hi')",
		"cntr_test",
		"hi\n",
		[]localCodeInterpreterGeneratedFile{
			{
				FileID:        "cfile_report",
				Filename:      "report.txt",
				Bytes:         12,
				BackingFileID: "file_backing",
				ContainerPath: "/workspace/report.txt",
				ContainerID:   "cntr_test",
			},
		},
		true,
		false,
		"completed",
	)
	require.NoError(t, err)

	payload := item.Map()
	outputs, ok := payload["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)

	logEntry, ok := outputs[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "logs", logEntry["type"])
	require.Equal(t, "hi\n", logEntry["logs"])
}
