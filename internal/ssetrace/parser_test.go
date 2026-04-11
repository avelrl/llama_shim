package ssetrace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseReaderParsesJSONEventsAndDoneSentinel(t *testing.T) {
	stream, err := ParseReader(strings.NewReader(strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","sequence_number":1}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","sequence_number":2}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")))
	require.NoError(t, err)

	require.True(t, stream.Done)
	require.Len(t, stream.Events, 3)
	require.Equal(t, "response.created", stream.Events[0].Event)
	require.Equal(t, `{"type":"response.created","sequence_number":1}`, stream.Events[0].Data)
	require.NotNil(t, stream.Events[0].JSON)
	require.Equal(t, "[DONE]", stream.Events[2].Data)
	require.Nil(t, stream.Events[2].JSON)
}

func TestParseReaderCombinesMultilineData(t *testing.T) {
	stream, err := ParseReader(strings.NewReader(strings.Join([]string{
		"event: note",
		"data: first line",
		"data: second line",
		"",
	}, "\n")))
	require.NoError(t, err)

	require.Len(t, stream.Events, 1)
	require.Equal(t, "first line\nsecond line", stream.Events[0].Data)
}

func TestParseReaderParsesIDAndRetry(t *testing.T) {
	stream, err := ParseReader(strings.NewReader(strings.Join([]string{
		"id: evt_123",
		"retry: 2500",
		`data: {"ok":true}`,
		"",
	}, "\n")))
	require.NoError(t, err)

	require.Len(t, stream.Events, 1)
	require.Equal(t, "evt_123", stream.Events[0].ID)
	require.NotNil(t, stream.Events[0].RetryMillis)
	require.Equal(t, 2500, *stream.Events[0].RetryMillis)
}
