package rag

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
)

var (
	thinkBlockPattern    = regexp.MustCompile(`(?s)<think>.*?</think>`)
	leadingThinkTailTrim = regexp.MustCompile(`(?s)^.*?</think>`)
)

func DetectQueryLanguage(text string) string {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return "zh"
		}
	}
	return "en"
}

func CleanModelOutput(raw string) string {
	cleaned := thinkBlockPattern.ReplaceAllString(raw, "")
	if strings.Contains(cleaned, "</think>") {
		cleaned = leadingThinkTailTrim.ReplaceAllString(cleaned, "")
	}
	return strings.TrimSpace(cleaned)
}

func FormatChatHistory(chatHistory []chat.ChatTurn, maxTurns int) string {
	if len(chatHistory) == 0 {
		return "None"
	}
	if maxTurns <= 0 {
		maxTurns = 5
	}

	start := 0
	limit := maxTurns * 2
	if len(chatHistory) > limit {
		start = len(chatHistory) - limit
	}

	lines := make([]string, 0, len(chatHistory[start:]))
	for _, turn := range chatHistory[start:] {
		content := turn.Content
		if turn.Role == "assistant" && len([]rune(content)) > 300 {
			runes := []rune(content)
			content = string(runes[:300]) + "..."
		}

		prefix := "Assistant"
		if turn.Role == "user" {
			prefix = "User"
		}
		lines = append(lines, prefix+": "+content)
	}

	return strings.Join(lines, "\n")
}
