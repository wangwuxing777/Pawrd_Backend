package rag

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

type markdownSection struct {
	Path string
	Text string
}

var (
	filenameZHPattern = regexp.MustCompile(`(^|[_\-\.])(zh|cn)([_\-\.]|$)`)
	filenameENPattern = regexp.MustCompile(`(^|[_\-\.])(en)([_\-\.]|$)`)
)

func detectDocumentLanguage(filename, text string) string {
	lowerName := strings.ToLower(filepath.Base(filename))
	if filenameZHPattern.MatchString(lowerName) {
		return "zh"
	}
	if filenameENPattern.MatchString(lowerName) {
		return "en"
	}

	total := 0
	cjk := 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		if unicode.Is(unicode.Han, r) {
			cjk++
		}
	}
	if total == 0 {
		return "en"
	}
	if float64(cjk)/float64(total) >= 0.05 {
		return "zh"
	}
	return "en"
}

func splitMarkdownSections(text string) []markdownSection {
	lines := strings.Split(text, "\n")
	sections := make([]markdownSection, 0, 16)

	var currentPath []string
	var buffer []string
	flush := func() {
		joined := strings.TrimSpace(strings.Join(buffer, "\n"))
		if joined == "" {
			return
		}
		sections = append(sections, markdownSection{
			Path: strings.Join(currentPath, " > "),
			Text: joined,
		})
		buffer = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			flush()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			title := strings.TrimSpace(trimmed[level:])
			if title != "" {
				if level-1 < len(currentPath) {
					currentPath = append(currentPath[:level-1], title)
				} else {
					for len(currentPath) < level-1 {
						currentPath = append(currentPath, "")
					}
					currentPath = append(currentPath, title)
				}
			}
			continue
		}
		buffer = append(buffer, line)
	}
	flush()
	if len(sections) == 0 && strings.TrimSpace(text) != "" {
		return []markdownSection{{Text: strings.TrimSpace(text)}}
	}
	return sections
}

func recursiveSplit(text string, chunkSize, chunkOverlap int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= chunkSize {
		return []string{text}
	}

	paragraphs := strings.Split(text, "\n\n")
	chunks := make([]string, 0, len(paragraphs))
	var current string
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}

		candidate := paragraph
		if current != "" {
			candidate = current + "\n\n" + paragraph
		}

		if len(candidate) <= chunkSize {
			current = candidate
			continue
		}

		if current != "" {
			chunks = append(chunks, current)
			current = withOverlap(current, chunkOverlap) + paragraph
			if len(current) > chunkSize {
				chunks = append(chunks, splitLongText(current, chunkSize, chunkOverlap)...)
				current = ""
			}
			continue
		}

		chunks = append(chunks, splitLongText(paragraph, chunkSize, chunkOverlap)...)
	}
	if strings.TrimSpace(current) != "" {
		chunks = append(chunks, strings.TrimSpace(current))
	}
	return chunks
}

func splitLongText(text string, chunkSize, chunkOverlap int) []string {
	if len(text) <= chunkSize {
		return []string{strings.TrimSpace(text)}
	}

	separators := []string{"\n", "。", "！", "？", "；", "，", "、", " "}
	chunks := make([]string, 0, 8)
	remaining := strings.TrimSpace(text)
	for len(remaining) > chunkSize {
		cut := chunkSize
		window := remaining[:chunkSize]
		for _, separator := range separators {
			if idx := strings.LastIndex(window, separator); idx >= chunkSize/2 {
				cut = idx + len(separator)
				break
			}
		}
		part := strings.TrimSpace(remaining[:cut])
		chunks = append(chunks, part)
		remaining = strings.TrimSpace(withOverlap(part, chunkOverlap) + remaining[cut:])
		if len(part) == 0 || len(remaining) == len(text) {
			break
		}
		text = remaining
	}
	if strings.TrimSpace(remaining) != "" {
		chunks = append(chunks, strings.TrimSpace(remaining))
	}
	return chunks
}

func withOverlap(text string, chunkOverlap int) string {
	if chunkOverlap <= 0 || len(text) <= chunkOverlap {
		return text
	}
	return text[len(text)-chunkOverlap:]
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aNorm, bNorm float64
	for i := range a {
		dot += a[i] * b[i]
		aNorm += a[i] * a[i]
		bNorm += b[i] * b[i]
	}
	if aNorm == 0 || bNorm == 0 {
		return 0
	}
	return dot / (sqrt(aNorm) * sqrt(bNorm))
}

func lexicalScore(query, text string) float64 {
	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return 0
	}
	textTerms := tokenize(text)
	if len(textTerms) == 0 {
		return 0
	}

	textSet := make(map[string]struct{}, len(textTerms))
	for _, term := range textTerms {
		textSet[term] = struct{}{}
	}

	matches := 0
	for _, term := range queryTerms {
		if _, ok := textSet[term]; ok {
			matches++
		}
	}
	score := float64(matches) / float64(len(queryTerms))
	score += keywordBonus(query, text)
	return score
}

func keywordBonus(query, text string) float64 {
	q := strings.ToLower(query)
	t := strings.ToLower(text)
	bonus := 0.0

	pairs := []struct {
		queryNeedles []string
		textNeedles  []string
		boost        float64
	}{
		{[]string{"waiting", "wait", "等候期"}, []string{"waiting period", "waiting", "等候期"}, 0.6},
		{[]string{"injury", "injuries", "受傷", "受伤"}, []string{"injury", "injuries", "accident", "受傷", "受伤"}, 0.35},
		{[]string{"chronic", "chronic medical conditions", "慢性"}, []string{"chronic", "慢性"}, 0.35},
	}

	for _, pair := range pairs {
		if containsAny(q, pair.queryNeedles...) && containsAny(t, pair.textNeedles...) {
			bonus += pair.boost
		}
	}
	return bonus
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func tokenize(text string) []string {
	if DetectQueryLanguage(text) == "zh" {
		return tokenizeChinese(text)
	}
	text = strings.ToLower(text)
	replacer := strings.NewReplacer(
		".", " ",
		",", " ",
		"?", " ",
		"!", " ",
		"(", " ",
		")", " ",
		":", " ",
		";", " ",
		"\n", " ",
		"\t", " ",
	)
	text = replacer.Replace(text)
	fields := strings.Fields(text)
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func tokenizeChinese(text string) []string {
	terms := make([]string, 0, len(text))
	runes := make([]rune, 0, len(text))
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			runes = append(runes, r)
			continue
		}
		if len(runes) > 0 {
			terms = append(terms, hanTerms(string(runes))...)
			runes = runes[:0]
		}
	}
	if len(runes) > 0 {
		terms = append(terms, hanTerms(string(runes))...)
	}
	return terms
}

func hanTerms(s string) []string {
	rs := []rune(s)
	if len(rs) == 0 {
		return nil
	}
	terms := []string{s}
	if len(rs) == 1 {
		return terms
	}
	for size := 2; size <= 3; size++ {
		if len(rs) < size {
			continue
		}
		for i := 0; i+size <= len(rs); i++ {
			terms = append(terms, string(rs[i:i+size]))
		}
	}
	return terms
}

func sqrt(v float64) float64 {
	guess := v
	if guess == 0 {
		return 0
	}
	for i := 0; i < 10; i++ {
		guess = (guess + v/guess) / 2
	}
	return guess
}
