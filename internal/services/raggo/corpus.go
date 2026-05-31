package raggo

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type Chunk struct {
	Text     string
	Metadata map[string]string
}

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*\S)\s*$`)
	anchorRe  = regexp.MustCompile(`^>\s*([A-Za-z][A-Za-z ]*):\s*(.*?)\s*$`)
)

func LoadChunks(cfg Config) ([]Chunk, error) {
	paths, err := markdownFiles(cfg.DataPath)
	if err != nil {
		return nil, err
	}
	chunks := make([]Chunk, 0, 512)
	for _, path := range paths {
		fileChunks, err := loadFileChunks(cfg, path)
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, fileChunks...)
	}
	return chunks, nil
}

func markdownFiles(root string) ([]string, error) {
	found := make([]string, 0, 16)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if strings.HasPrefix(name, ".") || name == "README.md" {
			return nil
		}
		if strings.EqualFold(filepath.Ext(name), ".md") {
			found = append(found, path)
		}
		return nil
	})
	sort.Strings(found)
	return found, err
}

func loadFileChunks(cfg Config, path string) ([]Chunk, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(raw)
	frontmatter, body := parseFrontmatter(text)

	provider := normalizeOptionalString(frontmatter["provider"])
	if provider == "" {
		provider = normalizeOptionalString(filepath.Base(filepath.Dir(path)))
	}
	if !contains(SupportedProviders, provider) {
		return nil, nil
	}

	language := normalizeOptionalString(frontmatter["language"])
	if language == "" {
		language = detectLanguage(path, body)
	}

	rel, _ := filepath.Rel(cfg.DataPath, path)
	baseMeta := map[string]string{
		"provider":             provider,
		"source_name":          filepath.Base(path),
		"source_path":          rel,
		"language":             language,
		"product":              frontmatter["product"],
		"policy_type":          frontmatter["policy_type"],
		"source_file":          frontmatter["source_file"],
		"source_version_label": frontmatter["source_version_label"],
		"normalization_status": frontmatter["normalization_status"],
		"schema_version":       frontmatter["schema_version"],
		"chunker_version":      ChunkerVersion,
	}

	return chunkMarkdownBody(body, baseMeta), nil
}

func parseFrontmatter(raw string) (map[string]string, string) {
	meta := make(map[string]string)
	if !strings.HasPrefix(raw, "---\n") {
		return meta, raw
	}
	rest := strings.TrimPrefix(raw, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return meta, raw
	}
	fm := rest[:idx]
	body := rest[idx+len("\n---\n"):]
	sc := bufio.NewScanner(strings.NewReader(fm))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if key != "" && val != "" && !strings.HasPrefix(val, "{") && !strings.HasPrefix(val, "[") {
			meta[key] = val
		}
	}
	return meta, body
}

func detectLanguage(path, body string) string {
	lower := strings.ToLower(filepath.Base(path))
	if strings.Contains(lower, "_zh") || strings.Contains(lower, "-zh") || strings.Contains(lower, ".zh") {
		return "zh"
	}
	if strings.Contains(lower, "_en") || strings.Contains(lower, "-en") || strings.Contains(lower, ".en") {
		return "en"
	}
	han := 0
	total := 0
	for _, r := range body {
		if r <= ' ' {
			continue
		}
		total++
		if r >= 0x4E00 && r <= 0x9FFF {
			han++
		}
	}
	if total == 0 {
		return "en"
	}
	if float64(han)/float64(total) >= 0.05 {
		return "zh"
	}
	return "en"
}

func chunkMarkdownBody(body string, baseMeta map[string]string) []Chunk {
	lines := strings.Split(body, "\n")
	headingPath := make([]string, 0, 6)
	curAnchor := make(map[string]string)
	sectionBuf := make([]string, 0, 32)
	chunks := make([]Chunk, 0, 64)

	flush := func() {
		text := strings.TrimSpace(strings.Join(sectionBuf, "\n"))
		sectionBuf = sectionBuf[:0]
		if text == "" {
			return
		}
		parts := splitByBudget(text, 1200)
		for i, part := range parts {
			m := copyMeta(baseMeta)
			m["chunk_index"] = strconv.Itoa(i)
			m["section_path"] = strings.Join(headingPath, " > ")
			m["clauses"] = curAnchor["clause"]
			m["unit_types"] = normalizeOptionalString(curAnchor["unit"])
			m["topic_tags"] = inferTopicTags(part, m["unit_types"])
			chunks = append(chunks, Chunk{
				Text:     part,
				Metadata: m,
			})
		}
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if m := headingRe.FindStringSubmatch(line); m != nil {
			flush()
			depth := len(m[1])
			title := strings.TrimSpace(m[2])
			if depth <= 0 {
				continue
			}
			if depth > len(headingPath) {
				headingPath = append(headingPath, title)
			} else {
				headingPath = append(headingPath[:depth-1], title)
			}
			curAnchor = make(map[string]string)
			continue
		}
		if m := anchorRe.FindStringSubmatch(line); m != nil {
			key := strings.ToLower(strings.TrimSpace(m[1]))
			val := strings.TrimSpace(m[2])
			curAnchor[key] = val
		}
		sectionBuf = append(sectionBuf, line)
	}
	flush()
	return chunks
}

func inferTopicTags(text, unitType string) string {
	l := strings.ToLower(text)
	tags := make([]string, 0, 4)
	if unitType != "" {
		tags = append(tags, unitType)
	}
	if strings.Contains(l, "waiting period") || strings.Contains(l, "等候期") {
		tags = append(tags, "waiting_period")
	}
	if strings.Contains(l, "consult") || strings.Contains(l, "診症") {
		tags = append(tags, "consult")
	}
	if strings.Contains(l, "plan a") || strings.Contains(l, "plan b") || strings.Contains(l, "hk$") {
		tags = append(tags, "limit")
	}
	uniq := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		uniq = append(uniq, t)
	}
	return strings.Join(uniq, ", ")
}

func splitByBudget(text string, maxChars int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}
	paras := strings.Split(text, "\n\n")
	out := make([]string, 0, 4)
	buf := strings.Builder{}
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		next := p
		if buf.Len() > 0 {
			next = buf.String() + "\n\n" + p
		}
		if len(next) <= maxChars {
			buf.Reset()
			buf.WriteString(next)
			continue
		}
		if buf.Len() > 0 {
			out = append(out, buf.String())
			buf.Reset()
		}
		if len(p) <= maxChars {
			buf.WriteString(p)
			continue
		}
		// Fallback hard split for very long paragraphs.
		start := 0
		for start < len(p) {
			end := start + maxChars
			if end > len(p) {
				end = len(p)
			}
			out = append(out, strings.TrimSpace(p[start:end]))
			start = end
		}
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}

func copyMeta(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
