package httpapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildLocalCodeInterpreterPlanningPromptUsesDockerBoundaryGuidance(t *testing.T) {
	t.Parallel()

	promptWithoutFiles := buildLocalCodeInterpreterPlanningPrompt(nil)
	require.Contains(t, promptWithoutFiles, "shim-managed Docker container")
	require.Contains(t, promptWithoutFiles, "no network access")
	require.Contains(t, promptWithoutFiles, "Do not assume any uploaded files are available")
	require.NotContains(t, promptWithoutFiles, "Do not access any filesystem paths")
	require.NotContains(t, promptWithoutFiles, "without filesystem, network, or subprocess access")

	promptWithFiles := buildLocalCodeInterpreterPlanningPrompt([]localCodeInterpreterInputFile{
		{WorkspaceName: "codes.txt", FileID: "file_codes"},
	})
	require.Contains(t, promptWithFiles, "Prefer reading the uploaded files already placed in the current working directory using relative paths.")
	require.Contains(t, promptWithFiles, "codes.txt (file_id=file_codes)")
	require.Contains(t, promptWithFiles, "Avoid depending on container system files or paths outside the current working directory")
}

func TestValidateLocalCodeInterpreterPlanCodeAllowsOrdinaryContainerPython(t *testing.T) {
	t.Parallel()

	err := validateLocalCodeInterpreterPlanCode("import os\nprint(os.getcwd())\n")
	require.NoError(t, err)
}

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
