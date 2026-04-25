package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"llama_shim/internal/config"
	"llama_shim/internal/llama"
)

type constrainedCustomToolRuntimeDeps struct {
	createChatCompletionText func(context.Context, []byte) (string, error)
}

type constrainedCustomToolBackendAdapter interface {
	Backend() string
	RuntimeFor(constrainedCustomToolRuntimeDeps, customToolDescriptor) (constrainedCustomToolRuntime, bool)
	Capability() constrainedCustomToolBackendCapability
}

type constrainedCustomToolBackendRegistry struct {
	adapters map[string]constrainedCustomToolBackendAdapter
}

type constrainedCustomToolBackendCapability struct {
	Support         string
	Runtime         string
	Backend         string
	CapabilityClass string
	NativeBackend   string
	NativeFormats   []string
	Validation      string
	Repair          string
	Routing         capabilityRouting
}

func defaultConstrainedCustomToolBackendRegistry() constrainedCustomToolBackendRegistry {
	return newConstrainedCustomToolBackendRegistry(vllmConstrainedCustomToolBackendAdapter{})
}

func newConstrainedCustomToolBackendRegistry(adapters ...constrainedCustomToolBackendAdapter) constrainedCustomToolBackendRegistry {
	registry := constrainedCustomToolBackendRegistry{
		adapters: make(map[string]constrainedCustomToolBackendAdapter, len(adapters)),
	}
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		backend := strings.ToLower(strings.TrimSpace(adapter.Backend()))
		if backend == "" {
			continue
		}
		registry.adapters[backend] = adapter
	}
	return registry
}

func (r constrainedCustomToolBackendRegistry) Adapter(backend string) (constrainedCustomToolBackendAdapter, bool) {
	adapter, ok := r.adapters[normalizeConstrainedDecodingBackend(backend)]
	return adapter, ok
}

func constrainedCustomToolBackendCapabilityFor(backend string) (constrainedCustomToolBackendCapability, bool) {
	adapter, ok := defaultConstrainedCustomToolBackendRegistry().Adapter(backend)
	if !ok {
		return constrainedCustomToolBackendCapability{}, false
	}
	return adapter.Capability(), true
}

type vllmConstrainedCustomToolBackendAdapter struct{}

func (vllmConstrainedCustomToolBackendAdapter) Backend() string {
	return config.ResponsesConstrainedDecodingBackendVLLM
}

func (vllmConstrainedCustomToolBackendAdapter) RuntimeFor(deps constrainedCustomToolRuntimeDeps, descriptor customToolDescriptor) (constrainedCustomToolRuntime, bool) {
	if descriptor.Constraint == nil {
		return nil, false
	}
	switch {
	case descriptor.Constraint.Syntax == "regex":
		return vllmRegexConstrainedCustomToolRuntime{
			createChatCompletionText: deps.createChatCompletionText,
		}, true
	case descriptor.Constraint.Syntax == "lark" && strings.TrimSpace(descriptor.Constraint.VLLMGrammar) != "":
		return vllmGrammarConstrainedCustomToolRuntime{
			createChatCompletionText: deps.createChatCompletionText,
		}, true
	default:
		return nil, false
	}
}

func (vllmConstrainedCustomToolBackendAdapter) Capability() constrainedCustomToolBackendCapability {
	return constrainedCustomToolBackendCapability{
		Support:         "grammar_native_with_validate_repair_fallback",
		Runtime:         "vllm_structured_outputs_regex_and_grammar",
		Backend:         "vllm",
		CapabilityClass: "grammar_native",
		NativeBackend:   "vllm",
		NativeFormats:   []string{"grammar.regex", "grammar.lark_subset"},
		Validation:      "native_regex_or_grammar_plus_local_guardrail",
		Repair:          "shim_validate_repair_after_native_invalid_timeout_or_upstream_error",
		Routing: capabilityRouting{
			PreferLocal:    "grammar_native_or_regex_native_or_shim_validate_repair_or_upstream_fallback",
			PreferUpstream: "proxy_first",
			LocalOnly:      "grammar_native_or_regex_native_or_shim_validate_repair_or_validation_error",
		},
	}
}

type fallbackConstrainedCustomToolRuntime struct {
	primary  constrainedCustomToolRuntime
	fallback constrainedCustomToolRuntime
}

var _ constrainedCustomToolRuntime = fallbackConstrainedCustomToolRuntime{}

func (r fallbackConstrainedCustomToolRuntime) Generate(ctx context.Context, request localConstrainedCustomToolRuntimeRequest) (string, error) {
	input, err := r.primary.Generate(ctx, request)
	if err == nil {
		return input, nil
	}
	if !shouldFallbackNativeConstrainedRuntimeError(err) || r.fallback == nil {
		return "", err
	}
	return r.fallback.Generate(ctx, request)
}

func shouldFallbackNativeConstrainedRuntimeError(err error) bool {
	var upstreamErr *llama.UpstreamError
	var timeoutErr *llama.TimeoutError
	var invalidErr *llama.InvalidResponseError
	return errors.As(err, &upstreamErr) || errors.As(err, &timeoutErr) || errors.As(err, &invalidErr)
}

func buildVLLMStructuredOutputsOptions(options map[string]json.RawMessage, field string, value string) (map[string]json.RawMessage, error) {
	cloned := cloneGenerationOptions(options)
	delete(cloned, "response_format")
	delete(cloned, "json_schema")
	delete(cloned, "structured_outputs")

	structuredOutputs := map[string]string{
		field: value,
	}
	rawStructuredOutputs, err := json.Marshal(structuredOutputs)
	if err != nil {
		return nil, err
	}
	cloned["structured_outputs"] = rawStructuredOutputs
	return cloned, nil
}
