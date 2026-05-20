package modelcert

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"llama_shim/internal/config"
)

func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse model certification manifest: %w", err)
	}
	if err := manifest.NormalizeAndValidate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m *Manifest) NormalizeAndValidate() error {
	if len(m.Models) == 0 {
		return fmt.Errorf("model certification manifest must contain at least one model")
	}
	seen := map[string]struct{}{}
	for idx := range m.Models {
		model := &m.Models[idx]
		model.Model = strings.TrimSpace(model.Model)
		model.Provider.ID = strings.TrimSpace(model.Provider.ID)
		model.Provider.BaseURL = strings.TrimRight(strings.TrimSpace(model.Provider.BaseURL), "/")
		model.Provider.BearerTokenEnv = strings.TrimSpace(model.Provider.BearerTokenEnv)
		model.Provider.UpstreamModel = strings.TrimSpace(model.Provider.UpstreamModel)
		model.Tester.Command = strings.TrimSpace(model.Tester.Command)
		model.Tester.Mode = defaultString(strings.TrimSpace(model.Tester.Mode), "compat")
		model.Tester.Gate = defaultString(strings.TrimSpace(model.Tester.Gate), "compat")
		model.Tester.Profile = strings.TrimSpace(model.Tester.Profile)
		model.Tester.ModelsConfig = strings.TrimSpace(model.Tester.ModelsConfig)
		model.Tester.SuiteConfig = strings.TrimSpace(model.Tester.SuiteConfig)
		model.Tester.CapabilitiesConfig = strings.TrimSpace(model.Tester.CapabilitiesConfig)
		model.Codex.ApplyPatchToolType = strings.TrimSpace(model.Codex.ApplyPatchToolType)
		model.Codex.ShellType = strings.TrimSpace(model.Codex.ShellType)
		model.Codex.ReasoningEffort = strings.TrimSpace(model.Codex.ReasoningEffort)
		for profileIdx := range model.Codex.Profiles {
			model.Codex.Profiles[profileIdx] = strings.TrimSpace(model.Codex.Profiles[profileIdx])
		}
		model.Codex.Profiles = compactStrings(model.Codex.Profiles)

		if model.Model == "" {
			return fmt.Errorf("model certification manifest: models[%d].model is required", idx)
		}
		if strings.ContainsAny(model.Model, " \t\r\n") || strings.Count(model.Model, "/") != 1 {
			return fmt.Errorf("model certification manifest: model %q must use provider/model form", model.Model)
		}
		if _, ok := seen[model.Model]; ok {
			return fmt.Errorf("model certification manifest: duplicate model %q", model.Model)
		}
		seen[model.Model] = struct{}{}
	}
	return nil
}

func SelectModels(manifest Manifest, wanted []string) ([]ModelEntry, error) {
	if len(wanted) == 0 {
		return append([]ModelEntry(nil), manifest.Models...), nil
	}
	wantedSet := map[string]struct{}{}
	for _, model := range wanted {
		model = strings.TrimSpace(model)
		if model != "" {
			wantedSet[model] = struct{}{}
		}
	}
	var selected []ModelEntry
	for _, model := range manifest.Models {
		if _, ok := wantedSet[model.Model]; ok {
			selected = append(selected, model)
			delete(wantedSet, model.Model)
		}
	}
	if len(wantedSet) > 0 {
		missing := make([]string, 0, len(wantedSet))
		for model := range wantedSet {
			missing = append(missing, model)
		}
		slices.Sort(missing)
		return nil, fmt.Errorf("model certification manifest does not contain requested model(s): %s", strings.Join(missing, ", "))
	}
	return selected, nil
}

func CompleteModelFromBase(model ModelEntry, cfg config.Config) (ModelEntry, error) {
	providerID, providerModel, ok := strings.Cut(model.Model, "/")
	if !ok || providerID == "" || providerModel == "" {
		return model, fmt.Errorf("model %q must use provider/model form", model.Model)
	}
	if model.Provider.ID == "" {
		model.Provider.ID = providerID
	}
	if model.Provider.ID != providerID {
		return model, fmt.Errorf("model %q provider id %q does not match public alias provider %q", model.Model, model.Provider.ID, providerID)
	}
	for _, provider := range cfg.LlamaProviders {
		if provider.ID != providerID {
			continue
		}
		if model.Provider.BaseURL == "" {
			model.Provider.BaseURL = provider.BaseURL
		}
		if model.Provider.BearerTokenEnv == "" {
			model.Provider.BearerTokenEnv = provider.BearerTokenEnv
		}
		for _, candidate := range provider.Models {
			if candidate.Model != providerModel {
				continue
			}
			if model.Provider.UpstreamModel == "" {
				model.Provider.UpstreamModel = candidate.UpstreamModel
			}
			break
		}
		break
	}
	if model.Provider.BaseURL == "" {
		return model, fmt.Errorf("model %q has no provider base_url in manifest or base config", model.Model)
	}
	if model.Provider.UpstreamModel == "" {
		model.Provider.UpstreamModel = providerModel
	}
	if !model.Codex.Skip && len(model.Codex.Profiles) == 0 {
		model.Codex.Profiles = []string{"baseline", "expanded", "bench-lite"}
	}
	if model.Codex.Attempts <= 0 {
		model.Codex.Attempts = 2
	}
	metadata := findCodexMetadata(cfg, model.Model)
	if model.Codex.ContextWindow <= 0 && metadata.ContextWindow > 0 {
		model.Codex.ContextWindow = metadata.ContextWindow
	}
	if model.Codex.ApplyPatchToolType == "" {
		model.Codex.ApplyPatchToolType = defaultString(metadata.ApplyPatchToolType, "freeform")
	}
	if model.Codex.ShellType == "" {
		model.Codex.ShellType = defaultString(metadata.ShellType, "shell_command")
	}
	if model.Codex.ReasoningEffort == "" {
		model.Codex.ReasoningEffort = "minimal"
	}
	return model, nil
}

func findCodexMetadata(cfg config.Config, model string) config.ResponsesCodexModelMetadata {
	for _, metadata := range cfg.ResponsesCodexModelMetadata {
		if metadata.Model == model {
			return metadata
		}
	}
	return config.ResponsesCodexModelMetadata{}
}

func compactStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
