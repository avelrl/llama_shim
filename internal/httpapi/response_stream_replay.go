package httpapi

import (
	"net/http"

	"llama_shim/internal/domain"
)

type responseReplayEmitter interface {
	Emit(responseReplayEvent) error
}

type responseReplayEmitterFunc func(responseReplayEvent) error

func (f responseReplayEmitterFunc) Emit(event responseReplayEvent) error {
	return f(event)
}

type responseReplayEmitOptions struct {
	IncludeObfuscation bool
	StartingAfter      int
	Profile            responseReplayProfile
}

type responseReplayEmitSummary struct {
	ReplayClass  string
	Capabilities []string
	EventCount   int
	LastEvent    string
	EmittedCount int
}

type sseResponseReplayEmitter struct {
	emitter *responseStreamEmitter
}

func newSSEResponseReplayEmitter(w http.ResponseWriter) (*sseResponseReplayEmitter, error) {
	emitter, err := newResponseStreamEmitter(w, false)
	if err != nil {
		return nil, err
	}
	return &sseResponseReplayEmitter{
		emitter: emitter,
	}, nil
}

func (e *sseResponseReplayEmitter) Emit(event responseReplayEvent) error {
	return e.emitter.write(event.eventType, event.payload)
}

func (e *sseResponseReplayEmitter) Done() error {
	return e.emitter.done()
}

func emitResponseReplayEvents(response domain.Response, artifacts []domain.ResponseReplayArtifact, options responseReplayEmitOptions, emitter responseReplayEmitter) (responseReplayEmitSummary, error) {
	profile := options.Profile
	summary := responseReplayEmitSummary{
		ReplayClass:  profile.replayClass,
		Capabilities: responseReplayCapabilityNames(profile.capabilities),
	}
	if err := forEachResponseReplayEvent(response, artifacts, options.IncludeObfuscation, profile, func(event responseReplayEvent) error {
		summary.EventCount++
		summary.LastEvent = event.eventType
		event.payload["sequence_number"] = summary.EventCount
		if options.StartingAfter > 0 && summary.EventCount <= options.StartingAfter {
			return nil
		}
		if err := emitter.Emit(event); err != nil {
			return err
		}
		summary.EmittedCount++
		return nil
	}); err != nil {
		return summary, err
	}
	return summary, nil
}

func responseReplayCapabilityNames(capabilities []responseReplayCapability) []string {
	if len(capabilities) == 0 {
		return nil
	}
	out := make([]string, 0, len(capabilities))
	seen := map[responseReplayCapability]bool{}
	for _, capability := range capabilities {
		if capability == "" || seen[capability] {
			continue
		}
		seen[capability] = true
		out = append(out, string(capability))
	}
	return out
}
