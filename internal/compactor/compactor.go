package compactor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

const (
	BackendHeuristic         = "heuristic"
	BackendModelAssistedText = "model_assisted_text"

	defaultTimeout         = 10 * time.Second
	defaultMaxOutputTokens = 1200
	defaultRetainedItems   = 8
	defaultMaxInputRunes   = 60000
)

type Config struct {
	Backend         string
	BaseURL         string
	Model           string
	Timeout         time.Duration
	MaxOutputTokens int
	RetainedItems   int
	MaxInputRunes   int
	Logger          *slog.Logger
}

type Result struct {
	Item     domain.Item
	Expanded []domain.Item
}

type Compactor interface {
	Compact(ctx context.Context, items []domain.Item) (Result, error)
}

func NormalizeConfig(cfg Config) (Config, error) {
	cfg.Backend = strings.ToLower(strings.TrimSpace(cfg.Backend))
	if cfg.Backend == "" {
		cfg.Backend = BackendHeuristic
	}
	switch cfg.Backend {
	case BackendHeuristic:
		cfg.BaseURL = ""
		cfg.Model = ""
		cfg.Timeout = 0
		cfg.MaxOutputTokens = 0
		cfg.RetainedItems = 0
		cfg.MaxInputRunes = 0
		return cfg, nil
	case BackendModelAssistedText:
	default:
		return Config{}, fmt.Errorf("unsupported compaction backend %q", cfg.Backend)
	}

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.BaseURL == "" {
		return Config{}, errors.New("responses.compaction.base_url must not be empty when responses.compaction.backend is model_assisted_text")
	}
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Model == "" {
		return Config{}, errors.New("responses.compaction.model must not be empty when responses.compaction.backend is model_assisted_text")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = defaultMaxOutputTokens
	}
	if cfg.RetainedItems < 0 {
		cfg.RetainedItems = 0
	}
	if cfg.RetainedItems == 0 {
		cfg.RetainedItems = defaultRetainedItems
	}
	if cfg.MaxInputRunes <= 0 {
		cfg.MaxInputRunes = defaultMaxInputRunes
	}
	return cfg, nil
}

func New(cfg Config) (Compactor, error) {
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	heuristic := Heuristic{}
	switch normalized.Backend {
	case BackendHeuristic:
		return heuristic, nil
	case BackendModelAssistedText:
		return &ModelAssistedText{
			cfg:      normalized,
			client:   llama.NewClient(normalized.BaseURL, normalized.Timeout),
			fallback: heuristic,
			logger:   cfg.Logger,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported compaction backend %q", normalized.Backend)
	}
}

type Heuristic struct{}

func (h Heuristic) Compact(ctx context.Context, items []domain.Item) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	summary := domain.BuildSyntheticCompactionSummary(items)
	item, err := domain.NewSyntheticCompactionItem(summary, len(items))
	if err != nil {
		return Result{}, err
	}
	expanded, err := domain.ExpandSyntheticCompactionItems([]domain.Item{item})
	if err != nil {
		return Result{}, err
	}
	return Result{Item: item, Expanded: expanded}, nil
}

type ModelAssistedText struct {
	cfg      Config
	client   *llama.Client
	fallback Compactor
	logger   *slog.Logger
}

type modelState struct {
	Summary         string          `json:"summary"`
	KeyFacts        modelStringList `json:"key_facts"`
	Constraints     modelStringList `json:"constraints"`
	OpenLoops       modelStringList `json:"open_loops"`
	RecentToolState modelStringList `json:"recent_tool_state"`
}

func (m *ModelAssistedText) Compact(ctx context.Context, items []domain.Item) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result, err := m.compactWithModel(ctx, items)
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	if m.logger != nil {
		m.logger.WarnContext(ctx, "model-assisted compaction failed; falling back to heuristic", "err", err)
	}
	return m.fallback.Compact(ctx, items)
}

func (m *ModelAssistedText) compactWithModel(ctx context.Context, items []domain.Item) (Result, error) {
	if m == nil || m.client == nil {
		return Result{}, errors.New("model-assisted compactor is nil")
	}

	modelCtx, cancel := context.WithTimeout(ctx, m.cfg.Timeout)
	defer cancel()

	transcript := domain.BuildSyntheticCompactionTranscript(items, len(items), m.cfg.MaxInputRunes)
	if strings.TrimSpace(transcript) == "" {
		transcript = "No prior context was provided."
	}
	messages := []domain.Item{
		domain.NewInputTextMessage("system", modelAssistedSystemPrompt()),
		domain.NewInputTextMessage("user", modelAssistedUserPrompt(transcript)),
	}
	options := map[string]json.RawMessage{
		"temperature":       json.RawMessage("0"),
		"max_output_tokens": json.RawMessage(fmt.Sprintf("%d", m.cfg.MaxOutputTokens)),
	}
	text, err := m.client.Generate(modelCtx, m.cfg.Model, messages, options)
	if err != nil {
		return Result{}, err
	}
	state, err := parseModelState(text)
	if err != nil {
		return Result{}, err
	}
	retained := retainRecentItems(items, m.cfg.RetainedItems)
	return buildStructuredResult(state, len(items), retained)
}

func buildStructuredResult(state domain.SyntheticCompactionState, itemCount int, retained []domain.Item) (Result, error) {
	summary := strings.TrimSpace(state.Summary)
	if summary == "" {
		summary = "Prior context was compacted."
	}
	item, err := domain.NewSyntheticCompactionItemWithOptions(summary, itemCount, domain.SyntheticCompactionOptions{
		Mode:          BackendModelAssistedText,
		State:         state,
		RetainedItems: retained,
	})
	if err != nil {
		return Result{}, err
	}
	expanded, err := domain.ExpandSyntheticCompactionItems([]domain.Item{item})
	if err != nil {
		return Result{}, err
	}
	return Result{Item: item, Expanded: expanded}, nil
}

func parseModelState(text string) (domain.SyntheticCompactionState, error) {
	raw := extractJSONObject(text)
	if len(raw) == 0 {
		return domain.SyntheticCompactionState{}, errors.New("compactor model did not return JSON")
	}

	var state modelState
	if err := json.Unmarshal(raw, &state); err != nil {
		return domain.SyntheticCompactionState{}, fmt.Errorf("decode compactor model JSON: %w", err)
	}
	if strings.TrimSpace(state.Summary) == "" {
		return domain.SyntheticCompactionState{}, errors.New("compactor model JSON omitted summary")
	}
	return domain.SyntheticCompactionState{
		Summary:         state.Summary,
		KeyFacts:        []string(state.KeyFacts),
		Constraints:     []string(state.Constraints),
		OpenLoops:       []string(state.OpenLoops),
		RecentToolState: []string(state.RecentToolState),
	}, nil
}

type modelStringList []string

func (l *modelStringList) UnmarshalJSON(raw []byte) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		*l = nil
		return nil
	}

	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return fmt.Errorf("expected array: %w", err)
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		value, err := modelStringListEntry(entry)
		if err != nil {
			return err
		}
		if value != "" {
			values = append(values, value)
		}
	}
	*l = values
	return nil
}

func modelStringListEntry(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text), nil
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", fmt.Errorf("expected string or object list entry: %w", err)
	}
	return modelStringFromObject(obj), nil
}

func modelStringFromObject(obj map[string]any) string {
	id := strings.TrimSpace(modelObjectStringField(obj, "id"))
	for _, key := range []string{"fact", "constraint", "task", "text", "value", "description", "summary", "state", "note"} {
		value := strings.TrimSpace(modelObjectStringField(obj, key))
		if value == "" {
			continue
		}
		if id != "" {
			return id + ": " + value
		}
		return value
	}

	keys := make([]string, 0, len(obj))
	for key := range obj {
		if key == "id" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(modelObjectStringField(obj, key))
		if value != "" {
			parts = append(parts, value)
		}
	}
	text := strings.Join(parts, "; ")
	if text != "" && id != "" {
		return id + ": " + text
	}
	return text
}

func modelObjectStringField(obj map[string]any, key string) string {
	value, ok := obj[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64, bool:
		return fmt.Sprint(typed)
	default:
		return ""
	}
}

func extractJSONObject(text string) []byte {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		trimmed = strings.TrimSuffix(trimmed, "```")
		trimmed = strings.TrimSpace(trimmed)
	}
	if json.Valid([]byte(trimmed)) {
		return []byte(trimmed)
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return nil
	}
	candidate := []byte(trimmed[start : end+1])
	if !json.Valid(candidate) {
		return nil
	}
	return candidate
}

func retainRecentItems(items []domain.Item, limit int) []domain.Item {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	start := len(items) - limit
	if start < 0 {
		start = 0
	}
	return append([]domain.Item(nil), items[start:]...)
}

func modelAssistedSystemPrompt() string {
	return strings.TrimSpace(`You compact prior conversation state for an OpenAI-compatible Responses API shim.
Return only a JSON object with these fields:
- summary: concise continuation summary.
- key_facts: array of strings with durable facts, IDs, decisions, names, paths, values, and user preferences.
- constraints: array of strings with active instructions, requirements, safety boundaries, and compatibility constraints.
- open_loops: array of strings with unresolved tasks, pending questions, and next actions.
- recent_tool_state: array of strings with recent tool calls, results, artifacts, errors, or external state needed to continue.

Rules:
- Preserve task-relevant state, not transcript trivia.
- Keep strings short and specific.
- Do not invent facts.
- Do not include markdown or prose outside the JSON object.`)
}

func modelAssistedUserPrompt(transcript string) string {
	var builder bytes.Buffer
	builder.WriteString("Compact these prior context items for continuation:\n\n")
	builder.WriteString(transcript)
	return builder.String()
}
