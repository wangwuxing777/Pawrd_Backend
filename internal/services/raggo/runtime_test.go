package raggo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProviderAndLanguage(t *testing.T) {
	if _, err := ValidateProvider("prudential"); err != nil {
		t.Fatalf("expected valid provider: %v", err)
	}
	if _, err := ValidateProvider("bolttech"); err == nil {
		t.Fatalf("expected invalid provider error")
	}
	if _, err := ValidateLanguage("zh"); err != nil {
		t.Fatalf("expected valid language: %v", err)
	}
	if _, err := ValidateLanguage("fr"); err == nil {
		t.Fatalf("expected invalid language error")
	}
}

func TestValidateMaxSources(t *testing.T) {
	got, err := ValidateMaxSources("", 6, 6)
	if err != nil || got != 6 {
		t.Fatalf("expected default max sources 6, got %d err=%v", got, err)
	}
	if _, err := ValidateMaxSources("0", 6, 6); err == nil {
		t.Fatalf("expected validation error for zero")
	}
}

func TestBuildExtractiveFallback(t *testing.T) {
	sources := []Source{
		{
			Provider:    "prudential",
			SourceName:  "sample.md",
			Clauses:     "1.C",
			SectionPath: "Benefits > Waiting Period",
			Snippet:     "Waiting period means claims can only start after the listed number of days.",
		},
	}

	answer := buildExtractiveFallback("what does waiting period mean", "prudential", sources)
	if !strings.Contains(answer, "sample.md") {
		t.Fatalf("expected source name in fallback answer, got %q", answer)
	}
	if !strings.Contains(answer, "Waiting period means claims can only start") {
		t.Fatalf("expected snippet in fallback answer, got %q", answer)
	}
}

func TestAnswerQueryFallsBackToExtractiveSummaryWhenLLMDisabled(t *testing.T) {
	cfg := LoadConfig()
	cfg.LLMBaseURL = ""
	cfg.LLMModel = ""
	cfg.LLMAPIKey = ""

	result := AnswerQuery(cfg, "What is the meaning of waiting period?", "", "", 3)
	if result.AnswerMode != "go_rag_source_summary_fallback" {
		t.Fatalf("expected extractive fallback mode, got %s answer=%q", result.AnswerMode, result.Answer)
	}
	if len(result.Sources) == 0 {
		t.Fatalf("expected retrieved sources")
	}
	if !strings.Contains(result.Answer, "retrieved policy snippets") {
		t.Fatalf("expected fallback framing, got %q", result.Answer)
	}
}

func TestAnswerQueryUsesLLMSummaryWhenConfigured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode summarizer request: %v", err)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) < 2 {
			t.Fatalf("expected chat messages, got %#v", payload)
		}
		msgMap, _ := messages[1].(map[string]any)
		content, _ := msgMap["content"].(string)
		if !strings.Contains(content, "Evidence snippets") {
			t.Fatalf("expected evidence prompt, got %q", content)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": "LLM grounded summary"},
			}},
		})
	}))
	defer server.Close()

	cfg := LoadConfig()
	cfg.LLMBaseURL = server.URL
	cfg.LLMModel = "test-model"
	cfg.LLMAPIKey = "test-key"
	cfg.LLMTimeoutSeconds = 5

	result := AnswerQuery(cfg, "What is the meaning of waiting period?", "", "", 3)
	if result.AnswerMode != "go_rag_llm_summary" {
		t.Fatalf("expected llm summary mode, got %s answer=%q", result.AnswerMode, result.Answer)
	}
	if strings.TrimSpace(result.Answer) != "LLM grounded summary" {
		t.Fatalf("unexpected llm answer: %q", result.Answer)
	}
	if len(result.Sources) == 0 {
		t.Fatalf("expected retrieved sources")
	}
}

func TestRerankerReordersCandidates(t *testing.T) {
	rerankServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode rerank request: %v", err)
		}
		if payload["query"] != "room and board" {
			t.Fatalf("unexpected rerank query: %#v", payload["query"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"index": 1, "relevance_score": 0.99},
				{"index": 0, "relevance_score": 0.10},
			},
		})
	}))
	defer rerankServer.Close()

	r := newReranker(Config{
		RerankEnabled:        true,
		RerankBaseURL:        rerankServer.URL,
		RerankModel:          "rerank-model",
		RerankAPIKey:         "rerank-key",
		RerankTopN:           2,
		RerankTimeoutSeconds: 5,
	})
	if r == nil {
		t.Fatalf("expected reranker")
	}

	candidates := []rankedChunk{
		{
			chunk: Chunk{
				Text: "first candidate",
				Metadata: map[string]string{
					"provider":     "prudential",
					"source_name":  "a.md",
					"section_path": "Section A",
				},
			},
			score: 1,
		},
		{
			chunk: Chunk{
				Text: "second candidate",
				Metadata: map[string]string{
					"provider":     "prudential",
					"source_name":  "b.md",
					"section_path": "Section B",
				},
			},
			score: 1,
		},
	}
	reordered, err := r.rerank("room and board", candidates)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(reordered) < 2 {
		t.Fatalf("expected reranked candidates")
	}
	if reordered[0].chunk.Metadata["source_name"] != "b.md" {
		t.Fatalf("expected reranked first source b.md, got %s", reordered[0].chunk.Metadata["source_name"])
	}
}

func TestLoadChunks_FromNormalizedCorpus(t *testing.T) {
	cfg := LoadConfig()
	chunks, err := LoadChunks(cfg)
	if err != nil {
		t.Fatalf("expected corpus to load, got error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatalf("expected non-empty chunk set from %s", cfg.DataPath)
	}
}

func TestLoadChunks_IncludesStructuredSummaryChunks(t *testing.T) {
	cfg := LoadConfig()
	chunks, err := LoadChunks(cfg)
	if err != nil {
		t.Fatalf("load chunks: %v", err)
	}

	var foundWaitingSummary bool
	var foundCoverageSummary bool
	for _, ch := range chunks {
		switch ch.Metadata["source_name"] {
		case "structured_waiting_period_summary":
			foundWaitingSummary = true
		case "structured_sub_coverage_summary":
			foundCoverageSummary = true
		}
		if foundWaitingSummary && foundCoverageSummary {
			break
		}
	}

	if !foundWaitingSummary {
		t.Fatalf("expected aggregated waiting period summary chunk")
	}
	if !foundCoverageSummary {
		t.Fatalf("expected aggregated coverage summary chunk")
	}
}

func TestLoadConfig_FallsBackToNormalizedCorpusWhenEnvPathMissing(t *testing.T) {
	original := os.Getenv("HK_INSURANCE_RAG_DATA_PATH")
	t.Cleanup(func() {
		_ = os.Setenv("HK_INSURANCE_RAG_DATA_PATH", original)
	})

	_ = os.Setenv("HK_INSURANCE_RAG_DATA_PATH", "assets/non_existing_corpus_path_for_test")
	cfg := LoadConfig()
	if !strings.Contains(filepath.ToSlash(cfg.DataPath), "assets/rag_normalized/hk_insurance") {
		t.Fatalf("expected fallback to normalized corpus path, got %s", cfg.DataPath)
	}
}

func TestDetectQueryIntent(t *testing.T) {
	intent := detectQueryIntent("What is the meaning of waiting period?")
	if !intent.isDefinition || intent.isComparison {
		t.Fatalf("unexpected definition intent: %+v", intent)
	}

	intent = detectQueryIntent("Compare Blue Cross and Prudential veterinary consultation limits.")
	if !intent.isComparison || intent.isDefinition {
		t.Fatalf("unexpected comparison intent: %+v", intent)
	}
}

func TestDiversifyCandidatesDefinitionPromotesDefinitionEvidence(t *testing.T) {
	candidates := []rankedChunk{
		{chunk: Chunk{Metadata: map[string]string{"unit_types": "benefit", "provider": "a", "section_path": "Benefits", "source_name": "benefit.md"}}, score: 10},
		{chunk: Chunk{Metadata: map[string]string{"unit_types": "definition", "provider": "b", "section_path": "Definitions", "source_name": "definition.md"}}, score: 9},
		{chunk: Chunk{Metadata: map[string]string{"unit_types": "waiting_period", "provider": "c", "section_path": "Structured Waiting", "source_name": "waiting.md"}}, score: 8},
	}

	got := diversifyCandidates(candidates, queryIntent{isDefinition: true})
	if len(got) < 2 {
		t.Fatalf("expected diversified candidates")
	}
	if got[0].chunk.Metadata["unit_types"] != "definition" {
		t.Fatalf("expected definition first, got %s", got[0].chunk.Metadata["unit_types"])
	}
	if got[1].chunk.Metadata["unit_types"] != "waiting_period" {
		t.Fatalf("expected waiting_period second, got %s", got[1].chunk.Metadata["unit_types"])
	}
}

func TestDiversifyCandidatesComparisonPromotesProviderCoverage(t *testing.T) {
	candidates := []rankedChunk{
		{chunk: Chunk{Metadata: map[string]string{"provider": "prudential", "section_path": "A", "source_name": "1.md"}}, score: 10},
		{chunk: Chunk{Metadata: map[string]string{"provider": "prudential", "section_path": "B", "source_name": "2.md"}}, score: 9},
		{chunk: Chunk{Metadata: map[string]string{"provider": "bluecross", "section_path": "C", "source_name": "3.md"}}, score: 8},
	}

	got := diversifyCandidates(candidates, queryIntent{isComparison: true})
	if len(got) < 2 {
		t.Fatalf("expected diversified candidates")
	}
	if got[0].chunk.Metadata["provider"] != "prudential" {
		t.Fatalf("expected first provider to preserve top-ranked source, got %s", got[0].chunk.Metadata["provider"])
	}
	if got[1].chunk.Metadata["provider"] != "bluecross" {
		t.Fatalf("expected second provider to diversify coverage, got %s", got[1].chunk.Metadata["provider"])
	}
}

func TestSelectTopCandidatesComparisonKeepsProviderCoverageWithinLimit(t *testing.T) {
	candidates := []rankedChunk{
		{chunk: Chunk{Metadata: map[string]string{"provider": "prudential", "section_path": "A", "source_name": "1.md"}}, score: 10},
		{chunk: Chunk{Metadata: map[string]string{"provider": "prudential", "section_path": "B", "source_name": "2.md"}}, score: 9},
		{chunk: Chunk{Metadata: map[string]string{"provider": "bluecross", "section_path": "C", "source_name": "3.md"}}, score: 8},
		{chunk: Chunk{Metadata: map[string]string{"provider": "bluecross", "section_path": "D", "source_name": "4.md"}}, score: 7},
	}

	got := selectTopCandidates(candidates, queryIntent{isComparison: true}, 3)
	if len(got) != 3 {
		t.Fatalf("expected 3 selected candidates, got %d", len(got))
	}
	if got[0].chunk.Metadata["provider"] != "prudential" {
		t.Fatalf("expected top-ranked provider first, got %s", got[0].chunk.Metadata["provider"])
	}
	if got[1].chunk.Metadata["provider"] != "bluecross" {
		t.Fatalf("expected second provider to preserve coverage within limit, got %s", got[1].chunk.Metadata["provider"])
	}
}
