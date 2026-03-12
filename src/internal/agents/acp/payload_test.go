package acp

import "testing"

func TestExtractRawText_JoinsContentArray(t *testing.T) {
	payload := map[string]any{
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": []any{
				map[string]any{"type": "text", "text": "你好"},
				map[string]any{"type": "text", "text": "，世界"},
			},
		},
	}

	raw, ok := extractRawText(payload)
	if !ok {
		t.Fatalf("expected content array to produce raw text")
	}
	if raw != "你好，世界" {
		t.Fatalf("unexpected raw text: %q", raw)
	}
}

func TestExtractRawText_ToolCallDoesNotPolluteAssistantOutput(t *testing.T) {
	payload := map[string]any{
		"update": map[string]any{
			"sessionUpdate": "tool_call",
			"title":         "Read task.md",
			"status":        "in_progress",
			"toolCallId":    "call_123",
		},
	}

	raw, ok := extractRawText(payload)
	if ok {
		t.Fatalf("expected tool call update to stay out of assistant output, got %q", raw)
	}
	if raw != "" {
		t.Fatalf("expected empty raw text for tool call, got %q", raw)
	}
}

func TestShouldAppendACPOutput_AppendsBehavioralUpdates(t *testing.T) {
	payload := map[string]any{
		"update": map[string]any{
			"sessionUpdate": "tool_call_update",
			"title":         "Read task.md",
		},
	}

	if !shouldAppendACPOutput(payload) {
		t.Fatalf("expected tool_call_update to append instead of replace")
	}
}

func TestShouldAppendACPOutput_DoesNotAppendTerminalMessage(t *testing.T) {
	payload := map[string]any{
		"update": map[string]any{
			"sessionUpdate": "agent_message",
			"text":          "done",
		},
		"stopReason": "end_turn",
	}

	if shouldAppendACPOutput(payload) {
		t.Fatalf("expected terminal payload to replace final output")
	}
}

func TestShouldUsePayloadTextForAssistantOutput_OnlyKeepsAgentMessage(t *testing.T) {
	if !shouldUsePayloadTextForAssistantOutput(map[string]any{
		"update": map[string]any{"sessionUpdate": "agent_message_chunk"},
	}) {
		t.Fatalf("expected agent_message_chunk to contribute assistant output")
	}
	if shouldUsePayloadTextForAssistantOutput(map[string]any{
		"update": map[string]any{"sessionUpdate": "tool_call"},
	}) {
		t.Fatalf("expected tool_call to stay out of assistant output")
	}
}

func TestBuildTaskEvent_ToolCallUsesStructuredFields(t *testing.T) {
	event, ok := buildTaskEvent(1, "session/update", map[string]any{
		"update": map[string]any{
			"sessionUpdate": "tool_call",
			"title":         "Run pwd",
			"status":        "in_progress",
			"toolCallId":    "call_123",
			"rawInput": map[string]any{
				"command": []any{"/bin/zsh", "-lc", "pwd"},
			},
		},
	})
	if !ok {
		t.Fatalf("expected tool_call to emit structured task event")
	}
	if event.Kind != "tool" {
		t.Fatalf("expected tool kind, got %q", event.Kind)
	}
	if event.ActivityKey != "call_123" {
		t.Fatalf("expected activity key from toolCallId, got %q", event.ActivityKey)
	}
	if event.Detail == "" {
		t.Fatalf("expected tool detail summary")
	}
}

func TestBuildTaskEvent_AvailableCommandsSummarizesNames(t *testing.T) {
	event, ok := buildTaskEvent(2, "session/update", map[string]any{
		"update": map[string]any{
			"sessionUpdate": "available_commands_update",
			"availableCommands": []any{
				map[string]any{"name": "review"},
				map[string]any{"name": "compact"},
			},
		},
	})
	if !ok {
		t.Fatalf("expected available_commands_update to emit task event")
	}
	if event.Kind != "command" {
		t.Fatalf("expected command kind, got %q", event.Kind)
	}
	if event.Detail != "review, compact" {
		t.Fatalf("unexpected command summary: %q", event.Detail)
	}
}
