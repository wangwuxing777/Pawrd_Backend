package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
)

type fakeEmbedder struct{}

func (fakeEmbedder) EmbedTexts(_ context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, 0, len(texts))
	for _, text := range texts {
		lower := strings.ToLower(text)
		vec := []float64{0, 0, 0}
		if strings.Contains(lower, "blue") || strings.Contains(lower, "藍十字") {
			vec[0] = 1
		}
		if strings.Contains(lower, "waiting") || strings.Contains(lower, "等候期") {
			vec[1] = 1
		}
		if strings.Contains(lower, "injury") || strings.Contains(lower, "受傷") {
			vec[2] = 1
		}
		result = append(result, vec)
	}
	return result, nil
}

type fakeCompleter struct{}

func (fakeCompleter) Complete(_ context.Context, _, userPrompt string) (string, error) {
	return "stub:" + userPrompt, nil
}

type blankCompleter struct{}

func (blankCompleter) Complete(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func TestLocalRuntimeProviderAndLanguageFiltering(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "bluecross", "blue_cross.md"), "# Terms\nBlue Cross waiting period for injury is 7 days.")
	mustWrite(t, filepath.Join(root, "bluecross", "blue_cross_zh.md"), "# 條款\n藍十字等候期：受傷 7 日。")
	mustWrite(t, filepath.Join(root, "prudential", "prudential.md"), "# Terms\nPrudential waiting period is 30 days.")

	runtime := newLocalRuntime(&config.Config{
		HKInsuranceRAGDataPath: root,
		HKInsuranceRAGTopK:     3,
	}, nil, fakeEmbedder{}, fakeCompleter{})

	resp, err := runtime.AskWithContext(context.Background(), ChatRequest{
		Query:       "Blue Cross waiting period for injury?",
		Provider:    "",
		SessionID:   "session-1",
		ChatHistory: []chat.ChatTurn{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("AskWithContext error: %v", err)
	}
	if resp.ActiveProvider != "bluecross" {
		t.Fatalf("expected active provider bluecross, got %q", resp.ActiveProvider)
	}
	if len(resp.Sources) == 0 || resp.Sources[0] != "bluecross (blue_cross.md)" {
		t.Fatalf("unexpected sources: %#v", resp.Sources)
	}

	respZH, err := runtime.AskWithContext(context.Background(), ChatRequest{
		Query:     "藍十字 等候期 幾耐？",
		SessionID: "session-2",
	})
	if err != nil {
		t.Fatalf("AskWithContext zh error: %v", err)
	}
	if len(respZH.Sources) == 0 || !strings.Contains(respZH.Sources[0], "blue_cross_zh.md") {
		t.Fatalf("expected zh source, got %#v", respZH.Sources)
	}
}

func TestLocalRuntimeNoDataFallback(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "bluecross", "blue_cross.md"), "# Terms\nBlue Cross waiting period for injury is 7 days.")

	runtime := newLocalRuntime(&config.Config{
		HKInsuranceRAGDataPath: root,
	}, nil, fakeEmbedder{}, fakeCompleter{})

	resp, err := runtime.AskWithContext(context.Background(), ChatRequest{
		Query:    "藍十字 等候期 幾耐？",
		Provider: "bluecross",
	})
	if err != nil {
		t.Fatalf("AskWithContext error: %v", err)
	}
	if len(resp.Sources) != 0 {
		t.Fatalf("expected no sources, got %#v", resp.Sources)
	}
	if !strings.Contains(resp.Answer, "中文保单内容") {
		t.Fatalf("expected zh no-data fallback, got %q", resp.Answer)
	}
}

func TestDescribeQuestionType(t *testing.T) {
	tests := map[string]string{
		"Blue Cross waiting period for injury?":    "waiting-period",
		"Does OneDegree cover chronic conditions?": "chronic-condition",
		"慢性疾病包唔包？":                                 "chronic-condition",
		"Which providers mention waiting periods?": "comparison",
		"What ages are eligible?":                  "age-eligibility",
	}

	for query, want := range tests {
		if got := describeQuestionType(query); got != want {
			t.Fatalf("describeQuestionType(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestBuildAnswerContextRanksRelevantEvidence(t *testing.T) {
	chunks := []indexedChunk{
		{
			Provider: "bluecross",
			Source:   "blue_cross.md",
			Text:     "Policy intro\nWaiting Periods\nWaiting period for injury is 7 days.\nGeneral company introduction.",
		},
		{
			Provider: "bluecross",
			Source:   "blue_cross.md",
			Text:     "Other benefits\nCoverage for surgery.",
		},
	}

	context := buildAnswerContext("Blue Cross waiting period for injury?", chunks)
	if !strings.Contains(context, "Waiting period for injury is 7 days.") {
		t.Fatalf("expected waiting period evidence in context, got %q", context)
	}
}

func TestBuildQuestionBriefWaitingPeriod(t *testing.T) {
	evidenceList := []evidence{
		{source: "bluecross (blue_cross.md)", provider: "bluecross", text: "Waiting Period (Bodily Injury): 7 days", score: 2},
		{source: "bluecross (blue_cross.md)", provider: "bluecross", text: "Waiting Period (Cancer): 90 days", score: 1},
	}

	got := buildQuestionBrief("Blue Cross waiting period for injury?", evidenceList)
	if !strings.Contains(got, "7 days") {
		t.Fatalf("expected injury waiting period in brief, got %q", got)
	}
}

func TestBuildQuestionBriefChronicCondition(t *testing.T) {
	evidenceList := []evidence{
		{source: "one_degree (one_degree_policy.md)", provider: "one_degree", text: "We will cover medical expenses for below Chronic Medical Conditions subject to the conditions set out in the following paragraphs.", score: 2},
		{source: "one_degree (one_degree_policy.md)", provider: "one_degree", text: "We will cover the above Chronic Medical Conditions ONLY IF both of the following conditions are met.", score: 1.8},
	}

	got := buildQuestionBrief("Does OneDegree cover chronic medical conditions?", evidenceList)
	if !strings.Contains(strings.ToLower(got), "chronic") {
		t.Fatalf("expected chronic-condition brief, got %q", got)
	}
}

func TestRetrieveAllProvidersKeepsTopKPerProvider(t *testing.T) {
	runtime := &localRuntime{
		cfg: &config.Config{HKInsuranceRAGTopK: 2},
		chunks: []indexedChunk{
			{Provider: "bluecross", Language: "zh", Source: "b1.md", Text: "慢性疾病 保障 條件 1"},
			{Provider: "bluecross", Language: "zh", Source: "b2.md", Text: "慢性疾病 保障 條件 2"},
			{Provider: "bluecross", Language: "zh", Source: "b3.md", Text: "慢性疾病 保障 條件 3"},
			{Provider: "one_degree", Language: "zh", Source: "o1.md", Text: "慢性疾病 保障 條件 A"},
			{Provider: "one_degree", Language: "zh", Source: "o2.md", Text: "慢性疾病 保障 條件 B"},
			{Provider: "one_degree", Language: "zh", Source: "o3.md", Text: "慢性疾病 保障 條件 C"},
		},
	}

	got, err := runtime.retrieve(context.Background(), "慢性疾病保障嗎", "zh", "")
	if err != nil {
		t.Fatalf("retrieve error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 chunks (2 per provider), got %d", len(got))
	}

	perProvider := map[string]int{}
	for _, chunk := range got {
		perProvider[chunk.Provider]++
	}
	if perProvider["bluecross"] != 2 || perProvider["one_degree"] != 2 {
		t.Fatalf("unexpected per-provider counts: %#v", perProvider)
	}
}

func TestBuildAnswerContextMultiProviderGroupsByProvider(t *testing.T) {
	chunks := []indexedChunk{
		{Provider: "bluecross", Source: "blue_cross_zh.md", Text: "慢性疾病 不保。"},
		{Provider: "one_degree", Source: "one_degree_policy_zh.md", Text: "慢性疾病 只在首個保單年度保障。"},
	}

	context := buildAnswerContext("保險涵蓋慢性疾病治療嗎", chunks)
	if !strings.Contains(context, "Provider: Blue Cross 藍十字") {
		t.Fatalf("expected bluecross provider section, got %q", context)
	}
	if !strings.Contains(context, "Provider: OneDegree") {
		t.Fatalf("expected one_degree provider section, got %q", context)
	}
}

func TestFallbackAnswerMultiProviderDoesNotUseSingleProductVoice(t *testing.T) {
	chunks := []indexedChunk{
		{Provider: "bluecross", Source: "blue_cross_zh.md", Text: "慢性疾病 不保。"},
		{Provider: "one_degree", Source: "one_degree_policy_zh.md", Text: "慢性疾病 只在首個保單年度保障。"},
	}

	got := fallbackAnswer("保險涵蓋慢性疾病治療嗎", chunks, true)
	if !strings.Contains(got, "Blue Cross 藍十字") || !strings.Contains(got, "OneDegree") {
		t.Fatalf("expected grouped providers in fallback answer, got %q", got)
	}
	if strings.Contains(got, "本產品") || strings.Contains(got, "本計劃") {
		t.Fatalf("fallback answer should not use single-product voice, got %q", got)
	}
}

func TestAskWithContextFallsBackWhenCompletionIsBlank(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "bluecross", "blue_cross_zh.md"), "# 條款\n慢性疾病 不保。")
	mustWrite(t, filepath.Join(root, "one_degree", "one_degree_policy_zh.md"), "# 條款\n慢性疾病 只在首個保單年度保障。")

	runtime := newLocalRuntime(&config.Config{
		HKInsuranceRAGDataPath: root,
		HKInsuranceRAGTopK:     2,
	}, nil, fakeEmbedder{}, blankCompleter{})

	resp, err := runtime.AskWithContext(context.Background(), ChatRequest{
		Query: "慢性病包含嗎",
	})
	if err != nil {
		t.Fatalf("AskWithContext error: %v", err)
	}
	if strings.TrimSpace(resp.Answer) == "" {
		t.Fatal("expected non-empty fallback answer when completer returns blank")
	}
	if !strings.Contains(resp.Answer, "Blue Cross 藍十字") || !strings.Contains(resp.Answer, "OneDegree") {
		t.Fatalf("expected grouped provider fallback answer, got %q", resp.Answer)
	}
}

func TestNewLocalRuntimeDoesNotStoreTypedNilEmbedderOrCompleter(t *testing.T) {
	runtime := newLocalRuntime(&config.Config{
		HKInsuranceRAGEmbeddingBaseURL: "https://api.siliconflow.cn/v1",
		HKInsuranceRAGEmbeddingAPIKey:  "",
		HKInsuranceRAGLLMBaseURL:       "https://api.siliconflow.cn/v1",
		HKInsuranceRAGLLMAPIKey:        "",
	}, nil, nil, nil)

	if runtime.embedder != nil {
		t.Fatal("expected embedder to stay nil when API key is missing")
	}
	if runtime.completer != nil {
		t.Fatal("expected completer to stay nil when API key is missing")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
