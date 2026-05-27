package main

import "testing"

func TestParseLogFields(t *testing.T) {
	fields := parseLogFields(`event.name="codex.sse_event" input_token_count=123 output_token_count=45 cached_token_count=100 conversation.id=abc`)

	if fields["event.name"] != "codex.sse_event" {
		t.Fatalf("event.name = %q", fields["event.name"])
	}
	if got := intField(fields, "input_token_count"); got != 123 {
		t.Fatalf("input_token_count = %d", got)
	}
	if fields["conversation.id"] != "abc" {
		t.Fatalf("conversation.id = %q", fields["conversation.id"])
	}
}

func TestEventTotalProviderSemantics(t *testing.T) {
	codex := tokenEvent{Provider: "codex", Input: 100, Output: 20, Cached: 80, Reasoning: 5, Tool: 10}
	if got := eventTotal(codex); got != 120 {
		t.Fatalf("codex total = %d", got)
	}

	claude := tokenEvent{Provider: "claude", Input: 100, Output: 20, Cached: 80}
	if got := eventTotal(claude); got != 200 {
		t.Fatalf("claude total = %d", got)
	}
}
