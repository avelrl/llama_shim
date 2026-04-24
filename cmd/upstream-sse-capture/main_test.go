package main

import (
	"testing"

	"llama_shim/internal/ssetrace"
)

func TestStreamHasTerminalEvent(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		if !streamHasTerminalEvent(ssetrace.Stream{
			Events: []ssetrace.Event{{Event: "response.completed"}},
		}) {
			t.Fatal("expected response.completed to be terminal")
		}
	})

	t.Run("failed", func(t *testing.T) {
		if !streamHasTerminalEvent(ssetrace.Stream{
			Events: []ssetrace.Event{{Event: "response.failed"}},
		}) {
			t.Fatal("expected response.failed to be terminal")
		}
	})

	t.Run("incomplete", func(t *testing.T) {
		if !streamHasTerminalEvent(ssetrace.Stream{
			Events: []ssetrace.Event{{Event: "response.incomplete"}},
		}) {
			t.Fatal("expected response.incomplete to be terminal")
		}
	})

	t.Run("non-terminal", func(t *testing.T) {
		if streamHasTerminalEvent(ssetrace.Stream{
			Events: []ssetrace.Event{{Event: "response.output_item.added"}},
		}) {
			t.Fatal("did not expect non-terminal event to count")
		}
	})
}

func TestErrorString(t *testing.T) {
	if got := errorString(nil); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
