package httpapi

import (
	"errors"
	"strings"
	"testing"

	"llama_shim/internal/domain"

	"github.com/stretchr/testify/require"
)

type recordingResponseReplayEmitter struct {
	events []responseReplayEvent
}

func (e *recordingResponseReplayEmitter) Emit(event responseReplayEvent) error {
	e.events = append(e.events, event)
	return nil
}

func TestEmitResponseReplayEventsAppliesStartingAfterWithoutRenumbering(t *testing.T) {
	recorder := &recordingResponseReplayEmitter{}
	response := testReplayResponse("resp_replay_starting_after", "OK")

	summary, err := emitResponseReplayEvents(response, nil, responseReplayEmitOptions{
		IncludeObfuscation: true,
		StartingAfter:      2,
		Profile:            retrieveResponseReplayProfile,
	}, recorder)
	require.NoError(t, err)
	require.Equal(t, "retrieve_stream_synthetic_replay", summary.ReplayClass)
	require.Contains(t, summary.Capabilities, string(responseReplayCapabilityGeneric))
	require.Greater(t, summary.EventCount, summary.EmittedCount)
	require.NotEmpty(t, recorder.events)
	require.Equal(t, 3, recorder.events[0].payload["sequence_number"])
	require.Equal(t, "response.output_item.added", recorder.events[0].eventType)
}

func TestForEachCompletedResponseReplayEventReportsReplayCapabilities(t *testing.T) {
	var events []responseReplayEvent

	response, summary, err := forEachCompletedResponseReplayEvent([]byte(`{
		"id":"resp_replay_completed",
		"object":"response",
		"created_at":1741900000,
		"status":"completed",
		"completed_at":1741900001,
		"model":"test-model",
		"background":false,
		"store":true,
		"text":{"format":{"type":"text"}},
		"usage":null,
		"metadata":{},
		"output_text":"OK",
		"output":[
			{
				"id":"msg_replay_completed",
				"type":"message",
				"role":"assistant",
				"status":"completed",
				"content":[
					{"type":"output_text","text":"OK","annotations":[]}
				]
			}
		]
	}`), customToolTransportPlan{}, false, responseReplayEmitterFunc(func(event responseReplayEvent) error {
		events = append(events, event)
		return nil
	}))
	require.NoError(t, err)
	require.Equal(t, "resp_replay_completed", response.ID)
	require.Equal(t, "create_stream_completed_response", summary.ReplayClass)
	require.Contains(t, summary.Capabilities, string(responseReplayCapabilityTypedText))
	require.Contains(t, summary.Capabilities, string(responseReplayCapabilityTypedToolFamily))
	require.Equal(t, summary.EventCount, summary.EmittedCount)
	require.Equal(t, summary.EventCount, len(events))
	require.Equal(t, "response.completed", summary.LastEvent)
	for idx, event := range events {
		require.Equal(t, idx+1, event.payload["sequence_number"])
	}
}

func TestEmitResponseReplayEventsShortCircuitsOnEmitterError(t *testing.T) {
	errStop := errors.New("stop after first event")
	response := testReplayResponse("resp_replay_short_circuit", strings.Repeat("x", 64*1024))

	summary, err := emitResponseReplayEvents(response, nil, responseReplayEmitOptions{
		IncludeObfuscation: true,
		Profile:            retrieveResponseReplayProfile,
	}, responseReplayEmitterFunc(func(responseReplayEvent) error {
		return errStop
	}))
	require.ErrorIs(t, err, errStop)
	require.Equal(t, 1, summary.EventCount)
	require.Equal(t, 0, summary.EmittedCount)
	require.Equal(t, "response.created", summary.LastEvent)
}

func testReplayResponse(id string, outputText string) domain.Response {
	completedAt := int64(1741900001)
	return domain.Response{
		ID:          id,
		Object:      "response",
		CreatedAt:   1741900000,
		Status:      "completed",
		CompletedAt: &completedAt,
		Model:       "test-model",
		Output:      []domain.Item{domain.NewOutputTextMessage(outputText)},
		OutputText:  outputText,
		Metadata:    map[string]string{},
	}
}
