package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type comfyUIProvider struct {
	baseURL       string
	client        *http.Client
	workflow      map[string]any
	outputNodeID  string
	pollInterval  time.Duration
	maxWait       time.Duration
	maxImageBytes int64
	counter       atomic.Uint64
}

type comfyUIImageRef struct {
	Filename  string
	Subfolder string
	Type      string
}

func newComfyUIProvider(cfg Config) (Provider, error) {
	workflow := cfg.ComfyUI.Workflow
	if cfg.ComfyUI.WorkflowPath != "" {
		raw, err := os.ReadFile(cfg.ComfyUI.WorkflowPath)
		if err != nil {
			return nil, fmt.Errorf("read ComfyUI workflow: %w", err)
		}
		if err := json.Unmarshal(raw, &workflow); err != nil {
			return nil, fmt.Errorf("decode ComfyUI workflow: %w", err)
		}
	}
	workflow, err := cloneWorkflowMap(workflow)
	if err != nil {
		return nil, err
	}
	return &comfyUIProvider{
		baseURL:       cfg.BaseURL,
		client:        &http.Client{Timeout: cfg.Timeout},
		workflow:      workflow,
		outputNodeID:  cfg.ComfyUI.OutputNodeID,
		pollInterval:  cfg.ComfyUI.PollInterval,
		maxWait:       cfg.ComfyUI.MaxWait,
		maxImageBytes: cfg.ComfyUI.MaxImageBytes,
	}, nil
}

func (p *comfyUIProvider) CheckReady(ctx context.Context) error {
	if p == nil || p.client == nil {
		return errors.New("image_generation provider is nil")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/system_stats", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ComfyUI readiness returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *comfyUIProvider) Create(ctx context.Context, requestBody []byte) ([]byte, error) {
	payload, err := p.createPayload(ctx, requestBody)
	if err != nil {
		return nil, err
	}
	return json.Marshal(payload.Response)
}

func (p *comfyUIProvider) CreateStream(ctx context.Context, requestBody []byte) (StreamResponse, error) {
	raw, err := p.Create(ctx, requestBody)
	if err != nil {
		return StreamResponse{}, err
	}
	return StreamResponse{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(raw)),
	}, nil
}

func (p *comfyUIProvider) createPayload(ctx context.Context, requestBody []byte) (fixturePayload, error) {
	var request map[string]any
	if err := json.Unmarshal(requestBody, &request); err != nil {
		return fixturePayload{}, fmt.Errorf("decode ComfyUI image_generation request: %w", err)
	}
	tool, err := singleImageGenerationTool(request["tools"])
	if err != nil {
		return fixturePayload{}, err
	}
	action := strings.ToLower(stringOrDefault(tool["action"], "generate"))
	if action == "edit" {
		return fixturePayload{}, errors.New("ComfyUI image_generation backend supports generate/auto only; edit requests require a workflow-specific image input adapter")
	}
	prompt := strings.TrimSpace(firstText(request["input"]))
	if prompt == "" {
		return fixturePayload{}, errors.New("ComfyUI image_generation backend requires text input")
	}

	n := p.counter.Add(1)
	responseID := fmt.Sprintf("resp_comfyui_image_%d", n)
	itemID := fmt.Sprintf("ig_comfyui_image_%d", n)
	width, height := parseImageSize(stringOrDefault(tool["size"], "1024x1024"))
	vars := map[string]any{
		"prompt":          prompt,
		"negative_prompt": "",
		"width":           width,
		"height":          height,
		"seed":            int64(n),
		"filename_prefix": fmt.Sprintf("llama_shim_%s", itemID),
	}

	workflow, err := renderComfyUIWorkflow(p.workflow, vars)
	if err != nil {
		return fixturePayload{}, err
	}
	promptID, err := p.submitPrompt(ctx, workflow, fmt.Sprintf("llama_shim_%d", n))
	if err != nil {
		return fixturePayload{}, err
	}
	imageRef, err := p.waitForImage(ctx, promptID)
	if err != nil {
		return fixturePayload{}, err
	}
	imageBytes, err := p.fetchImage(ctx, imageRef)
	if err != nil {
		return fixturePayload{}, err
	}

	createdAt := time.Now().Unix()
	item := map[string]any{
		"id":             itemID,
		"type":           "image_generation_call",
		"status":         "completed",
		"background":     stringOrDefault(tool["background"], "transparent"),
		"output_format":  stringOrDefault(tool["output_format"], "png"),
		"quality":        stringOrDefault(tool["quality"], "low"),
		"size":           fmt.Sprintf("%dx%d", width, height),
		"result":         base64.StdEncoding.EncodeToString(imageBytes),
		"revised_prompt": prompt,
		"action":         "generate",
	}
	response := map[string]any{
		"id":                   responseID,
		"object":               "response",
		"created_at":           createdAt,
		"status":               "completed",
		"completed_at":         createdAt,
		"error":                nil,
		"incomplete_details":   nil,
		"instructions":         request["instructions"],
		"max_output_tokens":    request["max_output_tokens"],
		"model":                stringOrDefault(request["model"], "comfyui-image-model"),
		"output":               []map[string]any{item},
		"parallel_tool_calls":  boolOrDefault(request["parallel_tool_calls"], true),
		"previous_response_id": request["previous_response_id"],
		"reasoning": map[string]any{
			"effort":  nil,
			"summary": nil,
		},
		"store":       false,
		"temperature": float64OrDefault(request["temperature"], 1.0),
		"text": map[string]any{
			"format": map[string]any{
				"type": "text",
			},
		},
		"tool_choice": map[string]any{
			"type": "image_generation",
		},
		"tools":       []map[string]any{tool},
		"top_p":       float64OrDefault(request["top_p"], 1.0),
		"truncation":  stringOrDefault(request["truncation"], "disabled"),
		"usage":       nil,
		"user":        request["user"],
		"metadata":    mapOrEmpty(request["metadata"]),
		"output_text": "",
	}
	return fixturePayload{Response: response, Item: item}, nil
}

func (p *comfyUIProvider) submitPrompt(ctx context.Context, workflow map[string]any, clientID string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"client_id": clientID,
		"prompt":    workflow,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/prompt", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	var decoded map[string]any
	if err := p.doJSON(req, &decoded); err != nil {
		return "", err
	}
	promptID := strings.TrimSpace(asString(decoded["prompt_id"]))
	if promptID == "" {
		return "", errors.New("ComfyUI /prompt response did not include prompt_id")
	}
	if nodeErrors, ok := decoded["node_errors"].(map[string]any); ok && len(nodeErrors) > 0 {
		return "", fmt.Errorf("ComfyUI rejected workflow with node_errors for prompt_id %s", promptID)
	}
	return promptID, nil
}

func (p *comfyUIProvider) waitForImage(ctx context.Context, promptID string) (comfyUIImageRef, error) {
	deadline := time.NewTimer(p.maxWait)
	defer deadline.Stop()
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		imageRef, done, err := p.historyImage(ctx, promptID)
		if err != nil {
			return comfyUIImageRef{}, err
		}
		if done {
			if imageRef.Filename == "" {
				return comfyUIImageRef{}, fmt.Errorf("ComfyUI prompt %s completed without image output", promptID)
			}
			return imageRef, nil
		}

		select {
		case <-ctx.Done():
			return comfyUIImageRef{}, ctx.Err()
		case <-deadline.C:
			return comfyUIImageRef{}, fmt.Errorf("ComfyUI prompt %s timed out after %s", promptID, p.maxWait)
		case <-ticker.C:
		}
	}
}

func (p *comfyUIProvider) historyImage(ctx context.Context, promptID string) (comfyUIImageRef, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/history/"+url.PathEscape(promptID), nil)
	if err != nil {
		return comfyUIImageRef{}, false, err
	}
	var decoded map[string]any
	if err := p.doJSON(req, &decoded); err != nil {
		return comfyUIImageRef{}, false, err
	}
	entry := decoded
	if nested, ok := decoded[promptID].(map[string]any); ok {
		entry = nested
	}
	if status, ok := entry["status"].(map[string]any); ok {
		statusText := strings.ToLower(asString(status["status_str"]))
		if strings.Contains(statusText, "error") || strings.Contains(statusText, "failed") {
			return comfyUIImageRef{}, false, fmt.Errorf("ComfyUI prompt %s failed with status %q", promptID, statusText)
		}
	}

	imageRef := p.extractHistoryImage(entry)
	if imageRef.Filename != "" {
		return imageRef, true, nil
	}
	completed := false
	if status, ok := entry["status"].(map[string]any); ok {
		completed, _ = status["completed"].(bool)
	}
	return comfyUIImageRef{}, completed, nil
}

func (p *comfyUIProvider) extractHistoryImage(entry map[string]any) comfyUIImageRef {
	outputs, ok := entry["outputs"].(map[string]any)
	if !ok {
		return comfyUIImageRef{}
	}
	if p.outputNodeID != "" {
		if ref := imageRefFromOutput(outputs[p.outputNodeID]); ref.Filename != "" {
			return ref
		}
		return comfyUIImageRef{}
	}
	keys := make([]string, 0, len(outputs))
	for key := range outputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if ref := imageRefFromOutput(outputs[key]); ref.Filename != "" {
			return ref
		}
	}
	return comfyUIImageRef{}
}

func imageRefFromOutput(output any) comfyUIImageRef {
	outputMap, ok := output.(map[string]any)
	if !ok {
		return comfyUIImageRef{}
	}
	images, ok := outputMap["images"].([]any)
	if !ok || len(images) == 0 {
		return comfyUIImageRef{}
	}
	image, ok := images[0].(map[string]any)
	if !ok {
		return comfyUIImageRef{}
	}
	return comfyUIImageRef{
		Filename:  strings.TrimSpace(asString(image["filename"])),
		Subfolder: strings.TrimSpace(asString(image["subfolder"])),
		Type:      stringOrDefault(image["type"], "output"),
	}
}

func (p *comfyUIProvider) fetchImage(ctx context.Context, image comfyUIImageRef) ([]byte, error) {
	values := url.Values{}
	values.Set("filename", image.Filename)
	values.Set("type", stringOrDefault(image.Type, "output"))
	if image.Subfolder != "" {
		values.Set("subfolder", image.Subfolder)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/view?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("ComfyUI /view returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, p.maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > p.maxImageBytes {
		return nil, fmt.Errorf("ComfyUI image output exceeded configured max_image_bytes %d", p.maxImageBytes)
	}
	return raw, nil
}

func (p *comfyUIProvider) doJSON(req *http.Request, target any) error {
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("ComfyUI %s returned HTTP %d: %s", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode ComfyUI %s response: %w", req.URL.Path, err)
	}
	return nil
}

func renderComfyUIWorkflow(workflow map[string]any, vars map[string]any) (map[string]any, error) {
	rendered, err := renderComfyUIValue(workflow, vars)
	if err != nil {
		return nil, err
	}
	out, ok := rendered.(map[string]any)
	if !ok {
		return nil, errors.New("ComfyUI workflow must render to an object")
	}
	return out, nil
}

func renderComfyUIValue(value any, vars map[string]any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			rendered, err := renderComfyUIValue(item, vars)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			rendered, err := renderComfyUIValue(item, vars)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil
	case string:
		return renderComfyUIString(typed, vars), nil
	default:
		return typed, nil
	}
}

func renderComfyUIString(value string, vars map[string]any) any {
	trimmed := strings.TrimSpace(value)
	for key, replacement := range vars {
		if trimmed == "{{"+key+"}}" {
			return replacement
		}
	}
	out := value
	for key, replacement := range vars {
		out = strings.ReplaceAll(out, "{{"+key+"}}", fmt.Sprint(replacement))
	}
	return out
}

func cloneWorkflowMap(workflow map[string]any) (map[string]any, error) {
	if len(workflow) == 0 {
		return nil, errors.New("ComfyUI workflow must not be empty")
	}
	raw, err := json.Marshal(workflow)
	if err != nil {
		return nil, err
	}
	var cloned map[string]any
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func parseImageSize(size string) (int, int) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 1024, 1024
	}
	width, errW := strconv.Atoi(strings.TrimSpace(parts[0]))
	height, errH := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return 1024, 1024
	}
	return width, height
}
