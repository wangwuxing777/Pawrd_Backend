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
