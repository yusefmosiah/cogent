package events

import (
	"encoding/json"
	"strings"
)

type Hint struct {
	Kind            string         `json:"kind"`
	Phase           string         `json:"phase"`
	NativeSessionID string         `json:"native_session_id,omitempty"`
	Payload         map[string]any `json:"payload"`
}

func TranslateLine(adapter, stream, line string) []Hint {
	var payload map[string]any
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return nil
	}

	var hints []Hint

	if nativeID := extractNativeSessionID(payload); nativeID != "" {
		hints = append(hints, Hint{
			Kind:            "session.discovered",
			Phase:           "translation",
			NativeSessionID: nativeID,
			Payload: map[string]any{
				"adapter":           adapter,
				"native_session_id": nativeID,
				"stream":            stream,
			},
		})
	}

	if delta := extractDelta(payload); delta != "" {
		hints = append(hints, Hint{
			Kind:  "assistant.delta",
			Phase: "translation",
			Payload: map[string]any{
				"text":   delta,
				"stream": stream,
			},
		})
	}

	if message := extractAssistantMessage(payload); message != "" {
		hints = append(hints, Hint{
			Kind:  "assistant.message",
			Phase: "translation",
			Payload: map[string]any{
				"text":   message,
				"stream": stream,
			},
		})
	}

	hints = append(hints, extractToolHints(payload)...)

	eventType := strings.ToLower(firstString(payload, "type", "event"))
	if strings.Contains(eventType, "error") {
		hints = append(hints, Hint{
			Kind:    "diagnostic",
			Phase:   "translation",
			Payload: payload,
		})
	}

	return hints
}

func extractNativeSessionID(payload map[string]any) string {
	if nativeID := firstString(payload, "session_id", "conversation_id", "thread_id"); nativeID != "" {
		return nativeID
	}

	if strings.ToLower(firstString(payload, "type")) == "session" {
		return firstString(payload, "id")
	}

	return ""
}

func extractToolHints(payload map[string]any) []Hint {
	var hints []Hint

	eventType := strings.ToLower(firstString(payload, "type", "event"))
	switch {
	case strings.Contains(eventType, "tool_call"), strings.Contains(eventType, "tool_use"):
		hints = append(hints, Hint{Kind: "tool.call", Phase: "execution", Payload: payload})
	case strings.Contains(eventType, "tool_result"), strings.Contains(eventType, "tool_response"):
		hints = append(hints, Hint{Kind: "tool.result", Phase: "execution", Payload: payload})
	}

	if item, ok := payload["item"].(map[string]any); ok {
		itemType := strings.ToLower(firstString(item, "type"))
		switch itemType {
		case "command_execution", "web_search", "collab_tool_call", "tool_call", "tool_use":
			hints = append(hints, Hint{Kind: "tool.call", Phase: "execution", Payload: item})
		case "command_execution_result", "web_search_result", "collab_tool_result", "tool_result", "tool_response":
			hints = append(hints, Hint{Kind: "tool.result", Phase: "execution", Payload: item})
		}
	}

	if message, ok := payload["message"].(map[string]any); ok {
		hints = append(hints, extractToolHintsFromContent(message["content"])...)
	}
	hints = append(hints, extractToolHintsFromContent(payload["content"])...)

	return hints
}

func extractToolHintsFromContent(value any) []Hint {
	blocks, ok := value.([]any)
	if !ok {
		return nil
	}

	var hints []Hint
	for _, item := range blocks {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		blockType := strings.ToLower(firstString(block, "type"))
		switch {
		case strings.Contains(blockType, "tool_use"):
			hints = append(hints, Hint{Kind: "tool.call", Phase: "execution", Payload: block})
		case strings.Contains(blockType, "tool_result"):
			hints = append(hints, Hint{Kind: "tool.result", Phase: "execution", Payload: block})
		}
	}

	return hints
}

func firstString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func extractDelta(payload map[string]any) string {
	if value := firstString(payload, "delta", "text_delta", "content_delta"); value != "" {
		return value
	}

	if event, ok := payload["assistantMessageEvent"].(map[string]any); ok {
		if value := firstString(event, "delta", "content"); value != "" {
			return value
		}
	}

	if delta, ok := payload["delta"].(map[string]any); ok {
		return firstString(delta, "text", "content")
	}

	return ""
}

func extractAssistantMessage(payload map[string]any) string {
	if item, ok := payload["item"].(map[string]any); ok {
		if strings.ToLower(firstString(item, "type")) == "agent_message" {
			if value := firstString(item, "text", "content", "message"); value != "" {
				return value
			}
		}
	}

	role := strings.ToLower(firstString(payload, "role"))
	if role == "assistant" {
		if value := firstString(payload, "content", "text", "message"); value != "" {
			return value
		}
	}

	if message, ok := payload["message"].(map[string]any); ok {
		if strings.ToLower(firstString(message, "role")) == "assistant" {
			if content := extractContent(message["content"]); content != "" {
				return content
			}
			if value := firstString(message, "text", "message"); value != "" {
				return value
			}
		}
	}

	if strings.Contains(strings.ToLower(firstString(payload, "type")), "assistant") {
		if value := firstString(payload, "content", "text", "message"); value != "" {
			return value
		}
		if content := extractContent(payload["content"]); content != "" {
			return content
		}
	}

	if strings.ToLower(firstString(payload, "type")) == "result" {
		if value := firstString(payload, "result"); value != "" {
			return value
		}
	}

	if completion, ok := payload["completion"].(map[string]any); ok {
		if value := firstString(completion, "finalText", "final_text", "text"); value != "" {
			return value
		}
	}

	if value := firstString(payload, "final_text", "finalText"); value != "" {
		return value
	}

	return ""
}

func extractContent(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		var parts []string
		for _, item := range typed {
			if block, ok := item.(map[string]any); ok {
				if text := firstString(block, "text", "content"); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		return firstString(typed, "text", "content")
	default:
		return ""
	}
}
