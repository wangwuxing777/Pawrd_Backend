package raggo

import (
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

func TestBuildDeterministicAnswer_WaitingPeriod(t *testing.T) {
	sources := []Source{
		{
			SourceName: "sample.md",
			Provider:   "prudential",
			Snippet:    "Waiting Period\nCancer: 180 days\nIllness: 30 days\nInjury: 7 days",
		},
	}
	intent := detectQueryIntent("What is the waiting period?", "")
	answer, mode, payload := buildDeterministicAnswer(intent, sources)
	if mode == "" || answer == "" {
		t.Fatalf("expected deterministic waiting period answer, got mode=%s answer=%q", mode, answer)
	}
	if mode != "deterministic_waiting_period_single" {
		t.Fatalf("expected waiting mode, got %s", mode)
	}
	if payload["type"] != "waiting_period_single" {
		t.Fatalf("expected waiting payload type, got %#v", payload["type"])
	}
}

func TestBuildDeterministicAnswer_WaitingPeriodDefinitionUsesEvidence(t *testing.T) {
	sources := []Source{
		{
			SourceName: "prudential.md",
			Provider:   "prudential",
			Clauses:    "4.1",
			Snippet:    "Waiting Period\nCancer: 180 days\nIllness: 30 days\nInjury: 7 days",
		},
		{
			SourceName: "msig.md",
			Provider:   "msig",
			Clauses:    "2.3",
			Snippet:    "Waiting Period\nIllness: 90 days\nInjury: 0 days",
		},
	}

	intent := detectQueryIntent("What does waiting period mean?", "")
	answer, mode, payload := buildDeterministicAnswer(intent, sources)
	if mode != "deterministic_waiting_period_single" {
		t.Fatalf("expected waiting definition mode, got mode=%s answer=%q", mode, answer)
	}
	if !strings.Contains(answer, "time that must pass before the listed condition can be claimed") {
		t.Fatalf("expected evidence-based definition lead, got %q", answer)
	}
	if !strings.Contains(answer, "Prudential: illness claims start after 30 days, cancer claims start after 180 days, injury claims start after 7 days") {
		t.Fatalf("expected Prudential evidence summary, got %q", answer)
	}
	if !strings.Contains(answer, "MSIG: illness claims start after 90 days, injury claims start after 0 days") {
		t.Fatalf("expected MSIG evidence summary, got %q", answer)
	}
	if payload["type"] != "waiting_period_single" {
		t.Fatalf("expected waiting payload type, got %#v", payload["type"])
	}
}

func TestBuildDeterministicAnswer_WaitingPeriodDefinitionSingleProviderUsesEvidence(t *testing.T) {
	sources := []Source{
		{
			SourceName: "bluecross.md",
			Provider:   "bluecross",
			Clauses:    "27",
			Snippet:    "Waiting Period\nIllness: 90 days",
		},
	}

	intent := detectQueryIntent("What is the meaning of waiting period?", "")
	answer, mode, _ := buildDeterministicAnswer(intent, sources)
	if mode != "deterministic_waiting_period_single" {
		t.Fatalf("expected waiting definition mode, got mode=%s answer=%q", mode, answer)
	}
	if !strings.Contains(answer, "time that must pass before the listed condition can be claimed") {
		t.Fatalf("expected evidence-based definition lead, got %q", answer)
	}
	if !strings.Contains(answer, "Blue Cross: illness claims start after 90 days") {
		t.Fatalf("expected provider evidence summary, got %q", answer)
	}
}

func TestBuildDeterministicAnswer_ConsultLimit(t *testing.T) {
	sources := []Source{
		{
			Provider:   "prudential",
			SourceName: "sample.md",
			Clauses:    "1.C",
			TopicTags:  "consult, limit",
			Snippet:    "Plan A: HK$8,000 per year (HK$400 per visit). Plan B: HK$16,000 per year (HK$800 per visit).",
		},
	}
	intent := detectQueryIntent("Prudential consultation limit?", "prudential")
	answer, mode, payload := buildDeterministicAnswer(intent, sources)
	if mode != "deterministic_consult_limit_single" {
		t.Fatalf("expected consult limit mode, got mode=%s answer=%q", mode, answer)
	}
	if payload["type"] != "consult_limit_single" {
		t.Fatalf("expected consult_limit_single type, got %#v", payload["type"])
	}
}

func TestBuildDeterministicAnswer_BenefitLimit(t *testing.T) {
	sources := []Source{
		{
			Provider:    "prudential",
			SourceName:  "sample.md",
			Clauses:     "1.B",
			TopicTags:   "limit, benefit",
			SectionPath: "Benefits > Room and Board",
			Snippet:     "Plan A: HK$3,500 per year (HK$250 per day). Plan B: HK$7,000 per year (HK$500 per day).",
		},
	}
	intent := detectQueryIntent("What is the room and board annual limit?", "prudential")
	answer, mode, payload := buildDeterministicAnswer(intent, sources)
	if mode != "deterministic_benefit_limit_single" {
		t.Fatalf("expected benefit limit mode, got mode=%s answer=%q", mode, answer)
	}
	if payload["type"] != "benefit_limit_single" {
		t.Fatalf("expected benefit_limit_single type, got %#v", payload["type"])
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
