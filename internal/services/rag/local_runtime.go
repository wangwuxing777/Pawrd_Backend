package rag

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/providercatalog"
	"gorm.io/gorm"
)

type embedder interface {
	EmbedTexts(ctx context.Context, texts []string) ([][]float64, error)
}

type completer interface {
	Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

type indexedChunk struct {
	ID        string
	Provider  string
	Source    string
	Language  string
	Section   string
	Text      string
	Embedding []float64
}

type scoredChunk struct {
	chunk indexedChunk
	score float64
}

type localRuntime struct {
	cfg       *config.Config
	embedder  embedder
	completer completer
	store     *dbStore

	mu     sync.RWMutex
	chunks []indexedChunk
}

func newLocalRuntime(cfg *config.Config, db *gorm.DB, embedder embedder, completer completer) *localRuntime {
	if cfg == nil {
		cfg = &config.Config{}
	}
	if embedder == nil && strings.TrimSpace(cfg.HKInsuranceRAGEmbeddingBaseURL) != "" {
		if resolved := newOpenAIEmbedder(cfg); resolved != nil {
			embedder = resolved
		}
	}
	if completer == nil && strings.TrimSpace(cfg.HKInsuranceRAGLLMBaseURL) != "" {
		if resolved := newOpenAICompleter(cfg); resolved != nil {
			completer = resolved
		}
	}
	return &localRuntime{
		cfg:       cfg,
		embedder:  embedder,
		completer: completer,
		store:     newDBStore(db),
	}
}

func (r *localRuntime) AskWithContext(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if isGreetingOrSmallTalk(req.Query) {
		return &ChatResponse{
			Answer:         buildSmallTalkResponse(req.Query),
			Sources:        []string{},
			ActiveProvider: "",
			SessionID:      req.SessionID,
		}, nil
	}

	if err := r.ensureIndex(ctx); err != nil {
		return nil, err
	}

	queryLanguage := DetectQueryLanguage(req.Query)
	effectiveProvider := providercatalog.NormalizeProviderID(req.Provider)
	if effectiveProvider == "" {
		effectiveProvider = providercatalog.DetectProvider(req.Query)
	}

	chunks, err := r.retrieve(ctx, req.Query, queryLanguage, effectiveProvider)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return &ChatResponse{
			Answer:         noDataMessage(queryLanguage, effectiveProvider),
			Sources:        []string{},
			ActiveProvider: effectiveProvider,
			SessionID:      req.SessionID,
		}, nil
	}

	sources := collectSources(chunks)
	contextStr := buildAnswerContext(req.Query, chunks)
	historyStr := FormatChatHistory(req.ChatHistory, 5)
	activeProviderName := "All Providers"
	if effectiveProvider != "" {
		activeProviderName = providercatalog.DisplayName(effectiveProvider)
	}

	answer, err := r.complete(ctx, contextStr, req.Query, historyStr, activeProviderName, chunks)
	if err != nil {
		return nil, err
	}
	answer = CleanModelOutput(answer)
	if answer == "" {
		answer = fallbackAnswer(req.Query, chunks, activeProviderName == "All Providers")
	}

	return &ChatResponse{
		Answer:         answer,
		Sources:        sources,
		ActiveProvider: effectiveProvider,
		SessionID:      req.SessionID,
	}, nil
}

func (r *localRuntime) ensureIndex(ctx context.Context) error {
	r.mu.RLock()
	if len(r.chunks) > 0 {
		r.mu.RUnlock()
		return nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.chunks) > 0 {
		return nil
	}

	chunks, err := r.loadOrBuildIndex(ctx)
	if err != nil {
		return err
	}
	r.chunks = chunks
	return nil
}

func (r *localRuntime) Rebuild(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.rebuildStore(ctx); err != nil {
		return err
	}
	if r.store != nil {
		chunks, err := r.store.LoadChunks(ctx)
		if err != nil {
			return err
		}
		r.chunks = chunks
		return nil
	}
	r.chunks = nil
	return nil
}

func (r *localRuntime) loadOrBuildIndex(ctx context.Context) ([]indexedChunk, error) {
	if r.store != nil {
		if r.cfg.HKInsuranceRAGRebuildOnStart {
			if err := r.rebuildStore(ctx); err != nil {
				return nil, err
			}
		}
		count, err := r.store.ChunkCount(ctx)
		if err != nil {
			return nil, err
		}
		if count == 0 {
			if err := r.rebuildStore(ctx); err != nil {
				return nil, err
			}
		}
		chunks, err := r.store.LoadChunks(ctx)
		if err != nil {
			return nil, err
		}
		if len(chunks) > 0 {
			return chunks, nil
		}
	}
	return r.buildIndexFromFiles(ctx)
}

func (r *localRuntime) buildIndexFromFiles(ctx context.Context) ([]indexedChunk, error) {
	dataPath := strings.TrimSpace(r.cfg.HKInsuranceRAGDataPath)
	if dataPath == "" {
		return nil, fmt.Errorf("HK insurance RAG data path is not configured")
	}

	rawChunks := make([]indexedChunk, 0, 128)
	err := filepath.WalkDir(dataPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		docChunks, err := r.loadDocument(path, dataPath)
		if err != nil {
			return err
		}
		rawChunks = append(rawChunks, docChunks...)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(rawChunks) == 0 {
		return []indexedChunk{}, nil
	}

	if r.embedder != nil {
		texts := make([]string, 0, len(rawChunks))
		indexes := make([]int, 0, len(rawChunks))
		for idx, chunk := range rawChunks {
			if strings.TrimSpace(chunk.Text) == "" {
				continue
			}
			texts = append(texts, chunk.Text)
			indexes = append(indexes, idx)
		}
		if len(texts) > 0 {
			embeddings, err := r.embedder.EmbedTexts(ctx, texts)
			if err != nil {
				return nil, err
			}
			for i, embedding := range embeddings {
				rawChunks[indexes[i]].Embedding = embedding
			}
		}
	}

	return rawChunks, nil
}

func (r *localRuntime) rebuildStore(ctx context.Context) error {
	if r.store == nil {
		return nil
	}
	dataPath := strings.TrimSpace(r.cfg.HKInsuranceRAGDataPath)
	if dataPath == "" {
		return fmt.Errorf("HK insurance RAG data path is not configured")
	}

	docs := make([]documentRecord, 0, 16)
	err := filepath.WalkDir(dataPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		doc, err := r.loadDocumentRecord(ctx, path, dataPath)
		if err != nil {
			return err
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return err
	}

	return r.store.Rebuild(ctx, dataPath, docs)
}

func (r *localRuntime) loadDocumentRecord(ctx context.Context, path, dataRoot string) (documentRecord, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return documentRecord{}, err
	}
	contentText := sanitizeUTF8(string(content))

	chunks, err := r.loadDocument(path, dataRoot)
	if err != nil {
		return documentRecord{}, err
	}

	relativePath, err := filepath.Rel(dataRoot, path)
	if err != nil {
		relativePath = path
	}
	provider := ""
	parts := strings.Split(relativePath, string(filepath.Separator))
	if len(parts) > 1 {
		provider = providercatalog.NormalizeProviderID(parts[0])
		if provider == "" {
			provider = parts[0]
		}
	}
	sourceName := filepath.Base(path)
	docType := "policy"
	if strings.Contains(strings.ToLower(sourceName), "plans_pricing") {
		docType = "pricing_table"
	}
	language := detectDocumentLanguage(sourceName, contentText)

	if r.embedder != nil {
		texts := make([]string, 0, len(chunks))
		for _, chunk := range chunks {
			texts = append(texts, chunk.Text)
		}
		if len(texts) > 0 {
			embeddings, err := r.embedder.EmbedTexts(ctx, texts)
			if err != nil {
				return documentRecord{}, err
			}
			for i := range chunks {
				if i < len(embeddings) {
					chunks[i].Embedding = embeddings[i]
				}
			}
		}
	}

	return documentRecord{
		Provider:    provider,
		SourcePath:  relativePath,
		SourceName:  sourceName,
		Language:    language,
		DocType:     docType,
		ContentHash: hashText(contentText),
		Chunks:      chunks,
	}, nil
}

func (r *localRuntime) loadDocument(path, dataRoot string) ([]indexedChunk, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	contentText := sanitizeUTF8(string(content))

	relativePath, err := filepath.Rel(dataRoot, path)
	if err != nil {
		relativePath = path
	}
	parts := strings.Split(relativePath, string(filepath.Separator))
	provider := ""
	if len(parts) > 1 {
		provider = providercatalog.NormalizeProviderID(parts[0])
		if provider == "" {
			provider = parts[0]
		}
	}

	sourceName := filepath.Base(path)
	language := detectDocumentLanguage(sourceName, contentText)
	sections := splitMarkdownSections(contentText)
	if strings.Contains(strings.ToLower(sourceName), "plans_pricing") {
		sections = []markdownSection{{Path: "", Text: contentText}}
	}

	chunks := make([]indexedChunk, 0, len(sections))
	for _, section := range sections {
		pieces := recursiveSplit(section.Text, 2000, 200)
		for idx, piece := range pieces {
			if strings.TrimSpace(piece) == "" {
				continue
			}
			text := strings.TrimSpace(piece)
			if section.Path != "" {
				text = section.Path + "\n" + text
			}
			chunks = append(chunks, indexedChunk{
				ID:       stableChunkID(provider, sourceName, language, section.Path, idx, text),
				Provider: provider,
				Source:   sourceName,
				Language: language,
				Section:  section.Path,
				Text:     text,
			})
		}
	}

	return chunks, nil
}

func (r *localRuntime) retrieve(ctx context.Context, query, language, provider string) ([]indexedChunk, error) {
	r.mu.RLock()
	chunks := make([]indexedChunk, 0, len(r.chunks))
	for _, chunk := range r.chunks {
		if chunk.Language != language {
			continue
		}
		if provider != "" && chunk.Provider != provider {
			continue
		}
		chunks = append(chunks, chunk)
	}
	r.mu.RUnlock()

	if len(chunks) == 0 {
		return nil, nil
	}

	var queryEmbedding []float64
	if r.embedder != nil {
		embeddings, err := r.embedder.EmbedTexts(ctx, []string{query})
		if err != nil {
			return nil, err
		}
		if len(embeddings) > 0 {
			queryEmbedding = embeddings[0]
		}
	}

	scoredChunks := make([]scoredChunk, 0, len(chunks))
	for _, chunk := range chunks {
		lexical := lexicalScore(query, chunk.Text)
		score := lexical
		if len(queryEmbedding) > 0 && len(chunk.Embedding) == len(queryEmbedding) {
			semantic := cosineSimilarity(queryEmbedding, chunk.Embedding)
			score = semantic*0.75 + lexical*0.25
		}
		scoredChunks = append(scoredChunks, scoredChunk{chunk: chunk, score: score})
	}

	sort.SliceStable(scoredChunks, func(i, j int) bool {
		return scoredChunks[i].score > scoredChunks[j].score
	})

	topK := r.cfg.HKInsuranceRAGTopK
	if topK <= 0 {
		topK = 6
	}
	if provider == "" {
		scoredChunks = selectTopKPerProvider(scoredChunks, topK)
	} else if len(scoredChunks) > topK {
		scoredChunks = scoredChunks[:topK]
	}

	result := make([]indexedChunk, 0, len(scoredChunks))
	for _, item := range scoredChunks {
		result = append(result, item.chunk)
	}
	return result, nil
}

func selectTopKPerProvider(items []scoredChunk, perProviderLimit int) []scoredChunk {
	if len(items) == 0 {
		return items
	}
	out := make([]scoredChunk, 0, len(items))
	perProvider := map[string]int{}
	for _, item := range items {
		if perProvider[item.chunk.Provider] >= perProviderLimit {
			continue
		}
		perProvider[item.chunk.Provider]++
		out = append(out, item)
	}
	if len(out) == 0 {
		return items
	}
	return out
}

func (r *localRuntime) complete(ctx context.Context, contextStr, question, historyStr, activeProviderName string, chunks []indexedChunk) (string, error) {
	if r.completer == nil {
		return fallbackAnswer(question, chunks, activeProviderName == "All Providers"), nil
	}

	systemPrompt := `You are the "Petwell AI Specialist," a professional expert on the Hong Kong pet insurance market. You provide answers based only on the retrieved policy evidence. Follow these rules:
- Reply in the same language as the user's question.
- Do not reveal internal reasoning or chain-of-thought.
- Be helpful, conversational, direct, and concise.
- Start with the direct answer first, not background.
- If the user asks a yes/no question, begin with "Yes", "No", or "Partly" when the evidence supports that.
- If the user asks about waiting periods, coverage limits, ages, reimbursement rates, or chronic-condition eligibility, surface the exact figure/rule in the first sentence.
- If multiple providers are relevant, compare them in bullets and name the provider for each point.
- If Provider context is "All Providers", organize the answer by provider and name the provider in every section.
- If Provider context is "All Providers", never write as if all evidence belongs to a single product or plan. Do not use phrases like "this product", "this plan", "本產品", or "本計劃".
- If the evidence is incomplete, say so briefly instead of guessing.
- Use bold text for dollar amounts and timeframes when relevant.
- Use bullet points when listing rules, limits, or comparisons.
- End with a short actionable suggestion.`

	userPrompt := fmt.Sprintf(`Provider context: %s
Question type hint: %s

Conversation History:
%s

Retrieved Policy Evidence:
%s

User Question:
%s`, activeProviderName, describeQuestionType(question), historyStr, contextStr, question)

	answer, err := r.completer.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return fallbackAnswer(question, chunks, activeProviderName == "All Providers"), nil
	}
	return answer, nil
}

func formatIndexedChunks(chunks []indexedChunk) string {
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		header := fmt.Sprintf("--- SOURCE: %s (%s, lang=%s) ---", chunk.Provider, chunk.Source, chunk.Language)
		parts = append(parts, header+"\n"+chunk.Text)
	}
	return strings.Join(parts, "\n\n")
}

func buildAnswerContext(question string, chunks []indexedChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	if hasMultipleProviders(chunks) {
		return buildMultiProviderAnswerContext(question, chunks)
	}
	evidenceList := buildEvidenceList(question, chunks, 10)
	brief := buildQuestionBrief(question, evidenceList)
	lines := make([]string, 0, len(evidenceList))
	if brief != "" {
		lines = append(lines, "Structured answer brief:")
		lines = append(lines, brief)
		lines = append(lines, "")
		lines = append(lines, "Supporting evidence:")
	}
	for _, item := range evidenceList {
		lines = append(lines, fmt.Sprintf("- [%s] %s", item.source, item.text))
	}
	return strings.Join(lines, "\n")
}

func buildMultiProviderAnswerContext(question string, chunks []indexedChunk) string {
	language := DetectQueryLanguage(question)
	lines := make([]string, 0, len(chunks)+8)
	if language == "zh" {
		lines = append(lines, "Structured answer brief:")
		lines = append(lines, "以下證據來自多間保險公司，請按保險公司分開理解與回答。")
	} else {
		lines = append(lines, "Structured answer brief:")
		lines = append(lines, "The evidence below comes from multiple providers. Summarize it provider by provider.")
	}

	for _, provider := range orderedProviders(chunks) {
		providerChunks := filterChunksByProvider(chunks, provider)
		if len(providerChunks) == 0 {
			continue
		}
		brief := buildQuestionBrief(question, buildEvidenceList(question, providerChunks, 6))
		lines = append(lines, "")
		lines = append(lines, "Provider: "+providercatalog.DisplayName(provider))
		if brief != "" {
			lines = append(lines, brief)
		}
		lines = append(lines, "Supporting evidence:")
		for _, item := range buildEvidenceList(question, providerChunks, 6) {
			lines = append(lines, fmt.Sprintf("- [%s] %s", item.source, item.text))
		}
	}
	return strings.Join(lines, "\n")
}

type evidence struct {
	source   string
	provider string
	text     string
	score    float64
}

func buildEvidenceList(question string, chunks []indexedChunk, limit int) []evidence {
	questionType := describeQuestionType(question)
	evidenceList := make([]evidence, 0, 24)
	seen := map[string]struct{}{}
	for _, chunk := range chunks {
		for _, line := range splitCandidateLines(chunk.Text) {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			key := chunk.Provider + "|" + chunk.Source + "|" + line
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			score := lexicalScore(question, line) + questionTypeBoost(questionType, question, line, chunk.Provider)
			if score == 0 && len(evidenceList) > 8 {
				continue
			}
			evidenceList = append(evidenceList, evidence{
				source:   fmt.Sprintf("%s (%s)", chunk.Provider, chunk.Source),
				provider: chunk.Provider,
				text:     line,
				score:    score,
			})
		}
	}

	sort.SliceStable(evidenceList, func(i, j int) bool {
		return evidenceList[i].score > evidenceList[j].score
	})

	if questionType == "comparison" {
		evidenceList = diversifyEvidenceByProvider(evidenceList, limit)
	} else if len(evidenceList) > limit {
		evidenceList = evidenceList[:limit]
	}
	return evidenceList
}

func describeQuestionType(question string) string {
	lower := strings.ToLower(question)
	switch {
	case isGreetingOrSmallTalk(question):
		return "greeting"
	case containsAny(lower, "compare", "which providers", "difference", "differences", "比較", "比较", "邊間", "边间", "邊個", "边个"):
		return "comparison"
	case containsAny(lower, "recommend", "recommendation", "推薦", "推荐", "推介") && containsAny(lower, "insurance", "保險", "保险"):
		return "recommendation"
	case containsAny(lower, "meaning of", "what is the meaning", "what does", "define", "意思", "是什麼", "是什么") &&
		containsAny(lower, "waiting", "wait", "等候期"):
		return "definition"
	case containsAny(lower, "waiting", "wait", "等候期"):
		return "waiting-period"
	case containsAny(lower, "chronic", "chronic condition", "chronic medical", "慢性"):
		return "chronic-condition"
	case containsAny(lower, "cover", "covered", "coverage", "保障", "包唔包", "包不包"):
		return "coverage"
	case containsAny(lower, "age", "years old", "年齡", "年龄", "歲", "岁", "幾歲", "几岁", "年紀", "年纪"):
		return "age-eligibility"
	default:
		return "general"
	}
}

func collectSources(chunks []indexedChunk) []string {
	seen := make(map[string]struct{}, len(chunks))
	sources := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		key := fmt.Sprintf("%s (%s)", chunk.Provider, chunk.Source)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		sources = append(sources, key)
	}
	sort.Strings(sources)
	return sources
}

func noDataMessage(language, provider string) string {
	if provider != "" {
		name := providercatalog.DisplayName(provider)
		if language == "zh" {
			return fmt.Sprintf("目前找不到 %s 的中文保单内容。请补充该保险商的中文文件后再提问。", name)
		}
		return fmt.Sprintf("I cannot find English policy content for %s yet. Please ingest English files for this provider.", name)
	}
	if language == "zh" {
		return "我目前找不到对应语言的保单内容，请先补充该语言的 markdown 文档后再提问。"
	}
	return "I cannot find policy content in the same language yet. Please ingest markdown documents for this language first."
}

func fallbackAnswer(question string, chunks []indexedChunk, multiProvider bool) string {
	if len(chunks) == 0 {
		return "I could not find relevant policy content."
	}
	if multiProvider || hasMultipleProviders(chunks) {
		return buildMultiProviderFallback(question, chunks)
	}
	candidates := buildEvidenceList(question, chunks, 6)
	if brief := buildQuestionBrief(question, candidates); brief != "" {
		return brief
	}

	lines := make([]string, 0, 4)
	maxLines := 3
	if DetectQueryLanguage(question) == "zh" {
		lines = append(lines, "根据检索到的保单内容：")
		for _, item := range candidates {
			if len(lines) > maxLines {
				break
			}
			lines = append(lines, "• "+item.text)
		}
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "Based on the retrieved policy content:")
	for _, item := range candidates {
		if len(lines) > maxLines {
			break
		}
		lines = append(lines, "- "+item.text)
	}
	return strings.Join(lines, "\n")
}

func buildMultiProviderFallback(question string, chunks []indexedChunk) string {
	language := DetectQueryLanguage(question)
	lines := make([]string, 0, len(chunks)+4)
	if language == "zh" {
		lines = append(lines, "以下為按保險公司分開整理的檢索結果：")
	} else {
		lines = append(lines, "Below is the retrieved evidence grouped by provider:")
	}
	for _, provider := range orderedProviders(chunks) {
		providerChunks := filterChunksByProvider(chunks, provider)
		if len(providerChunks) == 0 {
			continue
		}
		brief := buildQuestionBrief(question, buildEvidenceList(question, providerChunks, 6))
		if brief == "" {
			continue
		}
		if language == "zh" {
			lines = append(lines, "")
			lines = append(lines, providercatalog.DisplayName(provider)+"：")
		} else {
			lines = append(lines, "")
			lines = append(lines, providercatalog.DisplayName(provider)+":")
		}
		lines = append(lines, brief)
	}
	return strings.Join(lines, "\n")
}

func questionTypeBoost(questionType, question, line, provider string) float64 {
	lowerLine := strings.ToLower(line)
	boost := 0.0
	switch questionType {
	case "waiting-period":
		if containsAny(lowerLine, "waiting period", "waiting periods", "等候期") {
			boost += 1.2
		}
		if containsAny(lowerLine, "7 day", "7 days", "30 day", "30 days", "28-day", "28 day", "90 day", "90 days", "180 day", "180 days", "7天", "30天", "90天", "180天") {
			boost += 0.8
		}
		if containsAny(strings.ToLower(question), "injury", "injuries", "受傷", "受伤") && containsAny(lowerLine, "injury", "injuries", "accident", "受傷", "受伤") {
			boost += 0.5
		}
	case "chronic-condition":
		if containsAny(lowerLine, "chronic", "慢性") {
			boost += 1.0
		}
		if containsAny(lowerLine, "only if", "only", "条件", "只限", "only provided", "excluded", "不保") {
			boost += 0.5
		}
		if containsAny(lowerLine, "4 years old", "5 years old", "4 years", "5 years", "4歲", "5歲") {
			boost += 0.4
		}
	case "coverage":
		if containsAny(lowerLine, "we will cover", "covered", "不保", "保障", "cover", "coverage scope", "賠償", "赔偿") {
			boost += 0.7
		}
		if containsAny(strings.ToLower(question), "consult", "consultation", "vet", "veterinary", "診症", "诊症", "獸醫", "兽医") &&
			containsAny(lowerLine, "consult", "consultation", "vet", "veterinary", "診症", "诊症", "獸醫", "兽医") {
			boost += 0.9
		}
		if containsAny(strings.ToLower(question), "illness", "disease", "疾病") &&
			containsAny(lowerLine, "illness", "disease", "疾病", "injury") {
			boost += 0.4
		}
	case "comparison":
		boost += 0.2
		if containsAny(strings.ToLower(question), "waiting", "wait", "等候期") && containsAny(lowerLine, "waiting period", "waiting periods", "等候期") {
			boost += 0.8
		}
		if containsAny(strings.ToLower(question), "injury", "injuries", "受傷", "受伤") && containsAny(lowerLine, "injury", "injuries", "bodily injury", "accident", "受傷", "受伤") {
			boost += 0.5
		}
		if containsAny(strings.ToLower(question), "cover", "coverage", "保障", "包唔包", "包不包") && containsAny(lowerLine, "we will cover", "covered", "保障", "賠償", "赔偿") {
			boost += 0.6
		}
	case "age-eligibility", "recommendation":
		if containsAny(lowerLine, "6 months", "9 years", "8歲", "8岁", "6個月", "6个月", "4 years old", "5 years old", "below 6 years", "年齡", "年龄", "歲", "岁") {
			boost += 1.1
		}
		if containsAny(lowerLine, "must be", "at least", "less than", "or below", "投保申請時", "application", "申請時", "only if") {
			boost += 0.5
		}
	case "definition":
		if containsAny(lowerLine, "waiting period", "等候期", "specific number of days", "在保單生效日期", "policy commencement date", "保障將於相關等候期屆滿後") {
			boost += 1.0
		}
	}
	if provider != "" && containsAny(strings.ToLower(question), provider, providercatalog.DisplayName(provider)) {
		boost += 0.2
	}
	return boost
}

func diversifyEvidenceByProvider(items []evidence, limit int) []evidence {
	if len(items) <= limit {
		return items
	}
	out := make([]evidence, 0, limit)
	perProvider := map[string]int{}
	for _, item := range items {
		if len(out) >= limit {
			break
		}
		if perProvider[item.provider] >= 2 {
			continue
		}
		perProvider[item.provider]++
		out = append(out, item)
	}
	if len(out) < limit {
		seen := map[string]struct{}{}
		for _, item := range out {
			seen[item.source+"|"+item.text] = struct{}{}
		}
		for _, item := range items {
			if len(out) >= limit {
				break
			}
			key := item.source + "|" + item.text
			if _, ok := seen[key]; ok {
				continue
			}
			out = append(out, item)
		}
	}
	return out
}

func buildQuestionBrief(question string, evidenceList []evidence) string {
	switch describeQuestionType(question) {
	case "waiting-period":
		return buildWaitingBrief(question, evidenceList)
	case "coverage":
		return buildCoverageBrief(question, evidenceList)
	case "chronic-condition":
		return buildChronicBrief(question, evidenceList)
	case "comparison":
		return buildComparisonBrief(question, evidenceList)
	case "age-eligibility":
		return buildAgeEligibilityBrief(question, evidenceList)
	case "recommendation":
		return buildRecommendationBrief(question, evidenceList)
	case "definition":
		return buildDefinitionBrief(question, evidenceList)
	default:
		return ""
	}
}

func hasMultipleProviders(chunks []indexedChunk) bool {
	seen := map[string]struct{}{}
	for _, chunk := range chunks {
		if chunk.Provider == "" {
			continue
		}
		seen[chunk.Provider] = struct{}{}
		if len(seen) > 1 {
			return true
		}
	}
	return false
}

func orderedProviders(chunks []indexedChunk) []string {
	seen := map[string]struct{}{}
	providers := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.Provider == "" {
			continue
		}
		if _, ok := seen[chunk.Provider]; ok {
			continue
		}
		seen[chunk.Provider] = struct{}{}
		providers = append(providers, chunk.Provider)
	}
	sort.SliceStable(providers, func(i, j int) bool {
		return providercatalog.DisplayName(providers[i]) < providercatalog.DisplayName(providers[j])
	})
	return providers
}

func filterChunksByProvider(chunks []indexedChunk, provider string) []indexedChunk {
	filtered := make([]indexedChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.Provider == provider {
			filtered = append(filtered, chunk)
		}
	}
	return filtered
}

func buildWaitingBrief(question string, evidenceList []evidence) string {
	language := DetectQueryLanguage(question)
	injuryFocused := containsAny(strings.ToLower(question), "injury", "injuries", "bodily injury", "受傷", "受伤")

	lines := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, item := range evidenceList {
		lower := strings.ToLower(item.text)
		if !containsAny(lower, "waiting period", "waiting periods", "等候期") {
			continue
		}
		if injuryFocused && !containsAny(lower, "injury", "injuries", "bodily injury", "accident", "受傷", "受伤", "身體損傷", "身体损伤") {
			continue
		}
		key := item.text
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if language == "zh" {
			lines = append(lines, "• "+item.text)
		} else {
			lines = append(lines, "- "+item.text)
		}
		if len(lines) >= 3 {
			break
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if language == "zh" {
		return "根據檢索到的保單重點：\n" + strings.Join(lines, "\n")
	}
	return "Based on the retrieved policy highlights:\n" + strings.Join(lines, "\n")
}

func buildCoverageBrief(question string, evidenceList []evidence) string {
	language := DetectQueryLanguage(question)
	queryLower := strings.ToLower(question)
	positive := firstMatchingEvidence(evidenceList, func(item evidence) bool {
		lower := strings.ToLower(item.text)
		if !containsAny(lower, "we will cover", "covered", "保障", "賠償", "赔偿", "coverage scope") {
			return false
		}
		if containsAny(queryLower, "consult", "consultation", "vet", "veterinary", "診症", "诊症", "獸醫", "兽医") {
			return containsAny(lower, "consult", "consultation", "vet", "veterinary", "診症", "诊症", "獸醫", "兽医")
		}
		if containsAny(queryLower, "illness", "disease", "疾病") {
			return containsAny(lower, "illness", "disease", "疾病", "injury")
		}
		return true
	})
	negative := firstMatchingEvidence(evidenceList, func(item evidence) bool {
		lower := strings.ToLower(item.text)
		return containsAny(lower, "will not cover", "not covered", "不保", "不會賠償", "不会赔偿", "excluded")
	})

	lines := make([]string, 0, 3)
	if positive != nil {
		if language == "zh" {
			lines = append(lines, "• 承保重點："+positive.text)
		} else {
			lines = append(lines, "- Coverage rule: "+positive.text)
		}
	}
	if negative != nil {
		if language == "zh" {
			lines = append(lines, "• 不保／限制："+negative.text)
		} else {
			lines = append(lines, "- Limitation: "+negative.text)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if language == "zh" {
		return "根據檢索到的保單重點：\n" + strings.Join(lines, "\n")
	}
	return "Based on the retrieved policy highlights:\n" + strings.Join(lines, "\n")
}

func buildChronicBrief(question string, evidenceList []evidence) string {
	language := DetectQueryLanguage(question)
	lines := make([]string, 0, 4)
	for _, item := range evidenceList {
		lower := strings.ToLower(item.text)
		if !containsAny(lower, "chronic", "慢性") {
			continue
		}
		if containsAny(lower, "only if", "only", "must", "條件", "条件", "不獲賠償", "不获赔偿", "excluded", "not cover", "will cover") {
			if language == "zh" {
				lines = append(lines, "• "+item.text)
			} else {
				lines = append(lines, "- "+item.text)
			}
		}
		if len(lines) >= 3 {
			break
		}
	}
	if len(lines) == 0 {
		for _, item := range evidenceList[:minInt(len(evidenceList), 3)] {
			if language == "zh" {
				lines = append(lines, "• "+item.text)
			} else {
				lines = append(lines, "- "+item.text)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if language == "zh" {
		return "根據檢索到的慢性病相關條文：\n" + strings.Join(lines, "\n")
	}
	return "Based on the retrieved chronic-condition clauses:\n" + strings.Join(lines, "\n")
}

func buildComparisonBrief(question string, evidenceList []evidence) string {
	language := DetectQueryLanguage(question)
	grouped := make(map[string][]evidence)
	order := make([]string, 0, 4)
	for _, item := range evidenceList {
		if _, ok := grouped[item.provider]; !ok {
			order = append(order, item.provider)
		}
		grouped[item.provider] = append(grouped[item.provider], item)
	}
	if len(order) == 0 {
		return ""
	}

	lines := make([]string, 0, len(order)+2)
	if language == "zh" {
		lines = append(lines, fmt.Sprintf("根據檢索到的跨保險公司條文，共找到 %d 間相關保險公司：", len(order)))
	} else {
		lines = append(lines, fmt.Sprintf("Across the retrieved evidence, %d providers are relevant:", len(order)))
	}
	added := 0
	for _, provider := range order {
		items := grouped[provider]
		if len(items) == 0 {
			continue
		}
		best := items[0]
		name := providercatalog.DisplayName(provider)
		if language == "zh" {
			lines = append(lines, fmt.Sprintf("• %s：%s", name, best.text))
		} else {
			lines = append(lines, fmt.Sprintf("- %s: %s", name, best.text))
		}
		added++
		if added >= 4 {
			break
		}
	}
	return strings.Join(lines, "\n")
}

var agePattern = regexp.MustCompile(`(\d+)\s*(years? old|yrs?|歲|岁)`)

func buildAgeEligibilityBrief(question string, evidenceList []evidence) string {
	language := DetectQueryLanguage(question)
	ageYears, hasAge := extractAgeYears(question)
	best := firstMatchingEvidence(evidenceList, func(item evidence) bool {
		lower := strings.ToLower(item.text)
		return containsAny(lower, "6 months", "9 years", "8歲", "8岁", "6個月", "6个月", "4 years old", "5 years old", "below 6 years", "年齡", "年龄")
	})
	if best == nil {
		return ""
	}
	if hasAge && language == "zh" {
		if ageYears >= 9 {
			return fmt.Sprintf("如果你的寵物已經 **%d 歲**，按目前檢索到的藍十字條文，通常**不符合新投保年齡**。相關條文指出受保寵物於投保申請時的年齡必須介乎 **6 個月至 8 歲**。", ageYears)
		}
	}
	if hasAge && language != "zh" {
		if ageYears >= 9 {
			return fmt.Sprintf("If your pet is already **%d years old**, the retrieved Blue Cross clause suggests it is **not eligible for a new application**, because the application age must be between **6 months and 8 years**.", ageYears)
		}
	}
	if language == "zh" {
		return "根據檢索到的投保年齡條文：\n• " + best.text
	}
	return "Based on the retrieved age-eligibility clause:\n- " + best.text
}

func buildRecommendationBrief(question string, evidenceList []evidence) string {
	language := DetectQueryLanguage(question)
	if brief := buildAgeEligibilityBrief(question, evidenceList); brief != "" {
		if language == "zh" && strings.Contains(brief, "不符合新投保年齡") {
			return brief + "\n\n建議：可改問我其他保險公司的高齡寵物投保限制，或先比較是否只有續保而非新投保方案。"
		}
		if language != "zh" && strings.Contains(strings.ToLower(brief), "not eligible for a new application") {
			return brief + "\n\nSuggestion: ask me to compare other providers' senior-pet age limits, or check whether renewal is possible instead of a new application."
		}
	}
	return buildCoverageBrief(question, evidenceList)
}

func buildDefinitionBrief(question string, evidenceList []evidence) string {
	language := DetectQueryLanguage(question)
	if language == "zh" {
		return "「等候期」是指保單生效後的一段指定時間，在這段時間內，某些疾病或受傷相關索償**尚未開始受保障**。不同保險公司、不同病況的等候期長短可以不同，例如受傷可能是 **7 天**，而癌症可能更長。"
	}
	return "A **waiting period** is the fixed period after a policy starts during which certain claims are **not yet covered**. The exact duration depends on the provider and the type of condition—for example, injury may have a short waiting period, while cancer may have a longer one."
}

func extractAgeYears(question string) (int, bool) {
	matches := agePattern.FindStringSubmatch(question)
	if len(matches) < 2 {
		return 0, false
	}
	years, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, false
	}
	return years, true
}

func isGreetingOrSmallTalk(question string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(question))
	if trimmed == "" {
		return true
	}
	shortGreetings := []string{
		"hi", "hello", "hey", "yo", "你好", "您好", "哈囉", "哈喽", "在嗎", "在吗", "嗨", "hi!", "hello!",
	}
	for _, greeting := range shortGreetings {
		if trimmed == greeting {
			return true
		}
	}
	return false
}

func buildSmallTalkResponse(question string) string {
	if DetectQueryLanguage(question) == "zh" {
		return "你好！我是 Pawrd Assistant。你可以直接問我保險條款、等候期、投保年齡限制，或者讓我幫你比較不同保險公司的保障。"
	}
	return "Hi! I'm Pawrd Assistant. You can ask me about insurance terms, waiting periods, age limits, or ask me to compare different pet insurance providers."
}

func firstMatchingEvidence(items []evidence, predicate func(evidence) bool) *evidence {
	for _, item := range items {
		if predicate(item) {
			copy := item
			return &copy
		}
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func splitCandidateLines(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func stableChunkID(provider, source, language, section string, index int, text string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%s|%s|%s|%d|%s", provider, source, language, section, index, text)))
	return hex.EncodeToString(sum[:])
}
