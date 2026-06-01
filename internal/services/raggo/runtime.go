package raggo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Source struct {
	Provider    string  `json:"provider"`
	Language    string  `json:"language"`
	Product     string  `json:"product"`
	SourceName  string  `json:"source_name"`
	SectionPath string  `json:"section_path"`
	Clauses     string  `json:"clauses"`
	UnitTypes   string  `json:"unit_types"`
	TopicTags   string  `json:"topic_tags"`
	Score       float64 `json:"score"`
	Snippet     string  `json:"snippet"`
}

type AnswerResult struct {
	Question       string         `json:"question"`
	Provider       string         `json:"provider,omitempty"`
	Language       string         `json:"language,omitempty"`
	Intent         string         `json:"intent"`
	Answer         string         `json:"answer"`
	AnswerMode     string         `json:"answer_mode"`
	Structured     map[string]any `json:"structured_answer,omitempty"`
	Disclaimer     string         `json:"disclaimer"`
	Sources        []Source       `json:"sources"`
	ProcessingMS   int64          `json:"processing_ms"`
	Implementation string         `json:"implementation"`
}

type rankedChunk struct {
	chunk Chunk
	score float64
}

type llmSummarizer struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

type reranker struct {
	baseURL string
	apiKey  string
	model   string
	topN    int
	client  *http.Client
}

var tokenSplitRe = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func AnswerQuery(cfg Config, question, provider, language string, maxSources int) AnswerResult {
	started := time.Now()
	question = strings.TrimSpace(question)

	chunks, err := LoadChunks(cfg)
	if err != nil {
		return AnswerResult{
			Question:       question,
			Provider:       provider,
			Language:       language,
			Intent:         formatIntent(question, provider, language),
			Answer:         "RAG corpus loading failed in Go runtime: " + err.Error(),
			AnswerMode:     "go_error",
			Disclaimer:     defaultDisclaimer(),
			ProcessingMS:   time.Since(started).Milliseconds(),
			Implementation: "go_rag_llm_summary_v1",
		}
	}

	candidates := rankCandidates(chunks, question, provider, language, maxSources)
	candidates = rerankCandidates(cfg, question, candidates)
	sources := make([]Source, 0, len(candidates))
	for _, c := range candidates {
		sources = append(sources, buildSourcePayload(c.chunk, c.score))
	}
	if maxSources > 0 && len(sources) > maxSources {
		sources = sources[:maxSources]
	}

	answer, mode, structured := summarizeSources(cfg, question, provider, language, sources)
	if strings.TrimSpace(answer) == "" {
		answer = "No reliable evidence found in the current Go RAG retrieval for this query."
		mode = "go_no_evidence"
	}

	return AnswerResult{
		Question:       question,
		Provider:       provider,
		Language:       language,
		Intent:         formatIntent(question, provider, language),
		Answer:         answer,
		AnswerMode:     mode,
		Structured:     structured,
		Disclaimer:     defaultDisclaimer(),
		Sources:        sources,
		ProcessingMS:   time.Since(started).Milliseconds(),
		Implementation: "go_rag_llm_summary_v1",
	}
}

func summarizeSources(cfg Config, question, provider, language string, sources []Source) (string, string, map[string]any) {
	if len(sources) == 0 {
		return "No reliable evidence found in the current Go RAG retrieval for this query.", "go_no_evidence", nil
	}

	summarizer := newLLMSummarizer(cfg)
	if summarizer != nil {
		answer, err := summarizer.summarize(question, provider, language, sources)
		if err == nil && strings.TrimSpace(answer) != "" {
			return strings.TrimSpace(answer), "go_rag_llm_summary", map[string]any{
				"type":             "rag_llm_summary",
				"source_count":     len(sources),
				"summarizer_model": cfg.LLMModel,
			}
		}
	}

	return buildExtractiveFallback(question, provider, sources), "go_rag_source_summary_fallback", map[string]any{
		"type":         "rag_source_summary_fallback",
		"source_count": len(sources),
	}
}

func rerankCandidates(cfg Config, question string, candidates []rankedChunk) []rankedChunk {
	r := newReranker(cfg)
	if r == nil || len(candidates) < 2 {
		return candidates
	}
	reordered, err := r.rerank(question, candidates)
	if err != nil || len(reordered) == 0 {
		return candidates
	}
	return reordered
}

func newLLMSummarizer(cfg Config) *llmSummarizer {
	if strings.TrimSpace(cfg.LLMBaseURL) == "" || strings.TrimSpace(cfg.LLMModel) == "" || strings.TrimSpace(cfg.LLMAPIKey) == "" {
		return nil
	}
	timeout := time.Duration(cfg.LLMTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &llmSummarizer{
		baseURL: strings.TrimRight(cfg.LLMBaseURL, "/"),
		apiKey:  cfg.LLMAPIKey,
		model:   cfg.LLMModel,
		client:  &http.Client{Timeout: timeout},
	}
}

func newReranker(cfg Config) *reranker {
	if !cfg.RerankEnabled || strings.TrimSpace(cfg.RerankBaseURL) == "" || strings.TrimSpace(cfg.RerankModel) == "" || strings.TrimSpace(cfg.RerankAPIKey) == "" {
		return nil
	}
	timeout := time.Duration(cfg.RerankTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	topN := cfg.RerankTopN
	if topN <= 0 {
		topN = cfg.DefaultMaxSources
	}
	return &reranker{
		baseURL: strings.TrimRight(cfg.RerankBaseURL, "/"),
		apiKey:  cfg.RerankAPIKey,
		model:   cfg.RerankModel,
		topN:    topN,
		client:  &http.Client{Timeout: timeout},
	}
}

func (s *llmSummarizer) summarize(question, provider, language string, sources []Source) (string, error) {
	payload := map[string]any{
		"model":       s.model,
		"temperature": 0.1,
		"messages": []map[string]any{
			{
				"role": "system",
				"content": strings.Join([]string{
					"You are a grounded insurance RAG summarizer.",
					"Answer only from the provided evidence snippets.",
					"Do not invent benefits, limits, waiting periods, exclusions, or provider names that are not supported by the evidence.",
					"If the evidence is insufficient or conflicting, say so plainly.",
					"Use the same language as the user question when possible.",
					"Cite the supporting source names or clauses inline when useful.",
				}, " "),
			},
			{
				"role":    "user",
				"content": buildSummaryPrompt(question, provider, language, sources),
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 400 {
			msg = msg[:400] + "..."
		}
		return "", fmt.Errorf("status %d body=%s", resp.StatusCode, msg)
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("missing choices in summarizer response")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func (r *reranker) rerank(question string, candidates []rankedChunk) ([]rankedChunk, error) {
	documents := make([]string, 0, len(candidates))
	for _, c := range candidates {
		documents = append(documents, rerankDocument(c.chunk))
	}
	topN := r.topN
	if topN <= 0 || topN > len(documents) {
		topN = len(documents)
	}
	payload := map[string]any{
		"model":     r.model,
		"query":     question,
		"documents": documents,
		"top_n":     topN,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, r.baseURL+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return nil, fmt.Errorf("rerank status %d body=%s", resp.StatusCode, msg)
	}

	var parsed struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Results) == 0 {
		return nil, fmt.Errorf("empty rerank results")
	}

	used := map[int]bool{}
	out := make([]rankedChunk, 0, len(candidates))
	for _, item := range parsed.Results {
		if item.Index < 0 || item.Index >= len(candidates) || used[item.Index] {
			continue
		}
		used[item.Index] = true
		candidate := candidates[item.Index]
		candidate.score = candidate.score + item.RelevanceScore*10
		out = append(out, candidate)
	}
	for i, candidate := range candidates {
		if used[i] {
			continue
		}
		out = append(out, candidate)
	}
	return out, nil
}

func buildSummaryPrompt(question, provider, language string, sources []Source) string {
	var b strings.Builder
	b.WriteString("Question: ")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n")
	if provider = strings.TrimSpace(provider); provider != "" {
		b.WriteString("Requested provider filter: ")
		b.WriteString(provider)
		b.WriteString("\n")
	}
	if language = strings.TrimSpace(language); language != "" {
		b.WriteString("Requested language: ")
		b.WriteString(language)
		b.WriteString("\n")
	}
	b.WriteString("Evidence snippets:\n")
	for i, src := range sources {
		b.WriteString(fmt.Sprintf("[%d] provider=%s source=%s", i+1, valueOr(src.Provider, "unknown"), valueOr(src.SourceName, "unknown")))
		if src.Clauses != "" {
			b.WriteString(" clause=")
			b.WriteString(src.Clauses)
		}
		if src.SectionPath != "" {
			b.WriteString(" section=")
			b.WriteString(src.SectionPath)
		}
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(src.Snippet))
		b.WriteString("\n\n")
	}
	b.WriteString("Write a concise answer grounded only in the evidence above. If the evidence does not answer the question, say that explicitly.")
	return b.String()
}

func rerankDocument(ch Chunk) string {
	parts := []string{
		ch.Metadata["provider"],
		ch.Metadata["product"],
		ch.Metadata["source_name"],
		ch.Metadata["section_path"],
		ch.Metadata["clauses"],
		ch.Metadata["unit_types"],
		ch.Metadata["topic_tags"],
		ch.Text,
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func buildExtractiveFallback(question, provider string, sources []Source) string {
	lines := make([]string, 0, len(sources)+2)
	if strings.TrimSpace(provider) != "" {
		lines = append(lines, fmt.Sprintf("No LLM summary is available, so here are the top retrieved %s policy snippets for your question:", provider))
	} else {
		lines = append(lines, "No LLM summary is available, so here are the top retrieved policy snippets for your question:")
	}
	for i, src := range sources {
		label := strings.TrimSpace(src.SourceName)
		if label == "" {
			label = "snippet"
		}
		meta := make([]string, 0, 3)
		if src.Provider != "" {
			meta = append(meta, src.Provider)
		}
		if src.Clauses != "" {
			meta = append(meta, "clause "+src.Clauses)
		}
		if src.SectionPath != "" {
			meta = append(meta, src.SectionPath)
		}
		line := fmt.Sprintf("%d. %s", i+1, label)
		if len(meta) > 0 {
			line += " [" + strings.Join(meta, " | ") + "]"
		}
		line += ": " + compactSnippet(src.Snippet, 280)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func rankCandidates(chunks []Chunk, question, provider, language string, maxSources int) []rankedChunk {
	tokens := tokenize(question)
	out := make([]rankedChunk, 0, 64)
	for _, ch := range chunks {
		if provider != "" && ch.Metadata["provider"] != provider {
			continue
		}
		if language != "" && ch.Metadata["language"] != language {
			continue
		}
		searchText := strings.ToLower(strings.Join([]string{
			ch.Text,
			ch.Metadata["provider"],
			ch.Metadata["product"],
			ch.Metadata["source_name"],
			ch.Metadata["section_path"],
			ch.Metadata["clauses"],
			ch.Metadata["topic_tags"],
		}, "\n"))
		score := lexicalScore(searchText, tokens)
		score += metadataBonus(ch, tokens)
		if score <= 0 {
			continue
		}
		out = append(out, rankedChunk{chunk: ch, score: score})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if math.Abs(out[i].score-out[j].score) < 1e-6 {
			iSource := out[i].chunk.Metadata["source_name"] + "|" + out[i].chunk.Metadata["section_path"]
			jSource := out[j].chunk.Metadata["source_name"] + "|" + out[j].chunk.Metadata["section_path"]
			return iSource < jSource
		}
		return out[i].score > out[j].score
	})

	limit := 12
	if maxSources > 0 {
		limit = maxSources * 4
		if limit < 12 {
			limit = 12
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func tokenize(question string) []string {
	normalized := strings.ToLower(strings.TrimSpace(question))
	raw := tokenSplitRe.Split(normalized, -1)
	out := make([]string, 0, len(raw)*2)
	seen := map[string]bool{}
	for _, token := range raw {
		token = strings.TrimSpace(token)
		if token == "" || seen[token] {
			continue
		}
		if len(token) == 1 && !containsHan(token) {
			continue
		}
		seen[token] = true
		out = append(out, token)
	}
	for _, run := range hanRuns(normalized) {
		if len([]rune(run)) < 2 {
			if !seen[run] {
				seen[run] = true
				out = append(out, run)
			}
			continue
		}
		for _, token := range hanNGrams(run, 2, 4) {
			if seen[token] {
				continue
			}
			seen[token] = true
			out = append(out, token)
		}
	}
	return out
}

func lexicalScore(text string, tokens []string) float64 {
	if len(tokens) == 0 || strings.TrimSpace(text) == "" {
		return 0
	}
	score := 0.0
	for _, token := range tokens {
		count := strings.Count(text, token)
		if count == 0 {
			continue
		}
		score += 1 + math.Min(float64(count-1), 2)*0.25
	}
	return score
}

func metadataBonus(ch Chunk, tokens []string) float64 {
	provider := strings.ToLower(ch.Metadata["provider"])
	product := strings.ToLower(ch.Metadata["product"])
	sourceName := strings.ToLower(ch.Metadata["source_name"])
	sectionPath := strings.ToLower(ch.Metadata["section_path"])
	clauses := strings.ToLower(ch.Metadata["clauses"])
	unitTypes := strings.ToLower(ch.Metadata["unit_types"])
	topicTags := strings.ToLower(ch.Metadata["topic_tags"])
	fields := []string{provider, product, sourceName, sectionPath, clauses, unitTypes, topicTags}
	score := 0.0
	for _, token := range tokens {
		for i, field := range fields {
			if field == "" || !strings.Contains(field, token) {
				continue
			}
			switch i {
			case 3:
				score += 1.5
			case 6:
				score += 1.2
			default:
				score += 0.6
			}
		}
	}
	if containsAny(sectionPath, "claims provisions", "proof and documentation", "abandoned claims", "general conditions", "一般條款", "一般不保事項") {
		score -= 1.4
	}
	if unitTypes == "benefit" {
		score += 0.8
	}
	if unitTypes == "definition" {
		score += 0.35
	}
	if containsAny(sectionPath, "table of benefits", "annual limit", "獸醫診症", "veterinary consultation", "room and board", "住院費用") {
		score += 0.9
	}
	if containsAny(topicTags, "limit", "consult") {
		score += 0.6
	}
	return score
}

func buildSourcePayload(ch Chunk, score float64) Source {
	snippet := compactSnippet(ch.Text, 800)
	return Source{
		Provider:    ch.Metadata["provider"],
		Language:    ch.Metadata["language"],
		Product:     ch.Metadata["product"],
		SourceName:  ch.Metadata["source_name"],
		SectionPath: ch.Metadata["section_path"],
		Clauses:     ch.Metadata["clauses"],
		UnitTypes:   ch.Metadata["unit_types"],
		TopicTags:   ch.Metadata["topic_tags"],
		Score:       score,
		Snippet:     snippet,
	}
}

func compactSnippet(snippet string, limit int) string {
	snippet = strings.Join(strings.Fields(strings.TrimSpace(snippet)), " ")
	if limit > 0 && len(snippet) > limit {
		return snippet[:limit] + "..."
	}
	return snippet
}

func formatIntent(question, provider, language string) string {
	parts := []string{"retrieval"}
	if strings.TrimSpace(provider) != "" {
		parts = append(parts, "provider_filter")
	}
	if strings.TrimSpace(language) != "" {
		parts = append(parts, "language_filter")
	}
	if strings.TrimSpace(question) == "" {
		parts = append(parts, "empty_question")
	}
	return strings.Join(parts, ", ")
}

func defaultDisclaimer() string {
	return "仅供参考，不保证 100% 准确、完整或最新。最终以保险公司官网、正式保单、承保表、批单及最新书面说明为准。"
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func hanRuns(s string) []string {
	runs := make([]string, 0, 4)
	buf := make([]rune, 0, 16)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		runs = append(runs, string(buf))
		buf = buf[:0]
	}
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			buf = append(buf, r)
			continue
		}
		flush()
	}
	flush()
	return runs
}

func hanNGrams(s string, minN, maxN int) []string {
	runes := []rune(s)
	if len(runes) == 0 {
		return nil
	}
	if minN < 1 {
		minN = 1
	}
	if maxN < minN {
		maxN = minN
	}
	out := make([]string, 0, len(runes)*2)
	for n := minN; n <= maxN; n++ {
		if n > len(runes) {
			break
		}
		for i := 0; i+n <= len(runes); i++ {
			out = append(out, string(runes[i:i+n]))
		}
	}
	return out
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func containsAny(s string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}

func BuildCapabilities(cfg Config) map[string]any {
	meta := readIndexMetadata(cfg.PersistDir)
	return map[string]any{
		"ok":                  true,
		"service":             "go_rag_translation_skeleton",
		"supported_providers": SupportedProviders,
		"supported_languages": SupportedLanguages,
		"default_max_sources": cfg.DefaultMaxSources,
		"max_allowed_sources": cfg.MaxAllowedSources,
		"query_methods":       []string{"GET", "POST"},
		"query_routes": map[string]string{
			"healthz":      "/api/rag/go/healthz",
			"readyz":       "/api/rag/go/readyz",
			"capabilities": "/api/rag/go/capabilities",
			"query_get":    "/api/rag/go/query?q=...&provider=...&language=...",
			"query_post":   "/api/rag/go/query",
		},
		"summarization": map[string]any{
			"mode":                  "retrieval_plus_llm_summary",
			"llm_configured":        strings.TrimSpace(cfg.LLMBaseURL) != "" && strings.TrimSpace(cfg.LLMModel) != "" && strings.TrimSpace(cfg.LLMAPIKey) != "",
			"extractive_fallback":   true,
			"deterministic_removed": true,
		},
		"index": map[string]any{
			"persist_dir":                cfg.PersistDir,
			"chunker_version":            stringValue(meta, "chunker_version"),
			"document_count":             intValue(meta, "document_count"),
			"chunk_size":                 intValue(meta, "chunk_size"),
			"chunk_overlap":              intValue(meta, "chunk_overlap"),
			"data_path":                  stringValueWithDefault(meta, "data_path", cfg.DataPath),
			"built_at_utc":               stringValue(meta, "built_at_utc"),
			"source_markdown_file_count": intValue(meta, "source_markdown_file_count"),
			"supported_provider_count":   intValueWithDefault(meta, "supported_provider_count", len(SupportedProviders)),
			"chunker_version_go":         ChunkerVersion,
		},
	}
}

func readIndexMetadata(persistDir string) map[string]any {
	path := filepath.Join(persistDir, "prototype_index_meta.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return map[string]any{}
	}
	return parsed
}

func stringValue(meta map[string]any, key string) string {
	v, ok := meta[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func stringValueWithDefault(meta map[string]any, key, def string) string {
	if v := stringValue(meta, key); v != "" {
		return v
	}
	return def
}

func intValue(meta map[string]any, key string) int {
	v, ok := meta[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	default:
		return 0
	}
}

func intValueWithDefault(meta map[string]any, key string, def int) int {
	v := intValue(meta, key)
	if v == 0 {
		return def
	}
	return v
}
