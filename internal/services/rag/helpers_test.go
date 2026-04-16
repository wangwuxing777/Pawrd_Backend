package rag

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
)

func TestDetectQueryLanguage(t *testing.T) {
	if got := DetectQueryLanguage("What is the waiting period?"); got != "en" {
		t.Fatalf("expected en, got %q", got)
	}
	if got := DetectQueryLanguage("藍十字幾時生效"); got != "zh" {
		t.Fatalf("expected zh, got %q", got)
	}
}

func TestCleanModelOutput(t *testing.T) {
	raw := "<think>internal</think>\nAnswer"
	if got := CleanModelOutput(raw); got != "Answer" {
		t.Fatalf("expected cleaned answer, got %q", got)
	}

	raw = "junk</think>Visible"
	if got := CleanModelOutput(raw); got != "Visible" {
		t.Fatalf("expected leading junk removed, got %q", got)
	}
}

func TestFormatChatHistory(t *testing.T) {
	longAssistant := strings.Repeat("a", 305)
	history := []chat.ChatTurn{
		{Role: "user", Content: "Question 1"},
		{Role: "assistant", Content: "Answer 1"},
		{Role: "user", Content: "Question 2"},
		{Role: "assistant", Content: longAssistant},
	}

	got := FormatChatHistory(history, 2)
	if !strings.Contains(got, "User: Question 1") {
		t.Fatalf("expected first user turn in history, got %q", got)
	}
	if !strings.Contains(got, "Assistant: Answer 1") {
		t.Fatalf("expected assistant turn in history, got %q", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("expected long assistant turn to be truncated, got %q", got)
	}
}

func TestSanitizeUTF8(t *testing.T) {
	raw := string([]byte{'A', 0xa3, 'B'})
	got := sanitizeUTF8(raw)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8 after sanitization, got %q", got)
	}
	if !strings.Contains(got, "A") || !strings.Contains(got, "B") {
		t.Fatalf("expected surrounding content preserved, got %q", got)
	}
}
