package main

import (
	"testing"

	"llama_shim/internal/modelcert"
)

func TestApplyPhase(t *testing.T) {
	tests := []struct {
		name       string
		phase      string
		skipShim   bool
		skipTester bool
		skipCodex  bool
	}{
		{name: "full", phase: "full"},
		{name: "dry run", phase: "dry-run", skipShim: true, skipTester: true, skipCodex: true},
		{name: "api", phase: "api", skipCodex: true},
		{name: "codex", phase: "codex", skipTester: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts modelcert.RunOptions
			if err := applyPhase(tt.phase, &opts); err != nil {
				t.Fatal(err)
			}
			if opts.SkipShim != tt.skipShim || opts.SkipTester != tt.skipTester || opts.SkipCodex != tt.skipCodex {
				t.Fatalf("unexpected skips for phase %q: %#v", tt.phase, opts)
			}
		})
	}
}

func TestApplyPhaseRejectsUnknown(t *testing.T) {
	var opts modelcert.RunOptions
	if err := applyPhase("unknown", &opts); err == nil {
		t.Fatal("expected unknown phase error")
	}
}
