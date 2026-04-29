package codexeval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"sort"
	"strings"
)

type parsedEvent struct {
	Type string
	Item struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Status  string `json:"status"`
		Command string `json:"command"`
	} `json:"item"`
	Message string `json:"message"`
}

func parseCodexEvents(path string) ([]parsedEvent, CodexEventStats, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, CodexEventStats{}, "", err
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var events []parsedEvent
	typeSet := map[string]bool{}
	var stats CodexEventStats
	var finalText string
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var event parsedEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		events = append(events, event)
		stats.Total++
		typeSet[event.Type] = true
		switch event.Type {
		case "turn.completed":
			stats.TurnCompleted = true
		case "turn.failed":
			stats.TurnFailed = true
		}
		switch event.Item.Type {
		case "agent_message":
			if event.Type == "item.completed" {
				stats.AgentMessages++
				finalText = event.Item.Text
			}
		case "command_execution":
			if event.Type == "item.started" {
				stats.CommandStarted++
			}
			if event.Type == "item.completed" {
				stats.CommandComplete++
			}
		case "file_change":
			stats.FileChanges++
		case "tool_call", "mcp_tool_call", "web_search", "function_call":
			stats.ToolCalls++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, CodexEventStats{}, "", err
	}
	stats.Types = make([]string, 0, len(typeSet))
	for typ := range typeSet {
		stats.Types = append(stats.Types, typ)
	}
	sort.Strings(stats.Types)
	return events, stats, finalText, nil
}

func hasCodexEvent(events []parsedEvent, expected string) bool {
	eventType, itemType, hasItemType := strings.Cut(expected, ":")
	for _, event := range events {
		if hasItemType {
			if event.Type == eventType && event.Item.Type == itemType {
				return true
			}
			continue
		}
		if event.Type == expected || event.Item.Type == expected {
			return true
		}
	}
	return false
}
