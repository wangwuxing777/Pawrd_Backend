package rag

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

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

func NormalizeQueryText(text string) string {
	replacer := strings.NewReplacer(
		"claim", "索償",
		"claims", "索償",
		"consultation fee", "consultation 獸醫診症 診金",
		"consult fee", "consultation 獸醫診症 診金",
		"consultation", "consultation 獸醫診症 診金",
		"包唔包", "保障",
		"包不包", "保障",
		"有冇", "有沒有",
		"有无", "有沒有",
		"幾多日", "多少日",
		"幾耐", "多久",
	)
	return strings.TrimSpace(replacer.Replace(text))
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

func sanitizeUTF8(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	return strings.ToValidUTF8(text, "�")
}
