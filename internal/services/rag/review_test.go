package rag

import "testing"

func TestReviewQueryMarksGreetingAsFallback(t *testing.T) {
	review := reviewQuery("你好呀", "")
	if review.Route != "fallback" {
		t.Fatalf("expected fallback route, got %q", review.Route)
	}
	if review.QueryType != "small_talk" {
		t.Fatalf("expected small_talk type, got %q", review.QueryType)
	}
}

func TestReviewQueryUsesAllProvidersForComparison(t *testing.T) {
	review := reviewQuery("比較 OneDegree、Blue Cross 同 Prudential 嘅受傷等候期。", "")
	if review.Route != "rag" {
		t.Fatalf("expected rag route, got %q", review.Route)
	}
	if review.QueryType != "comparison" {
		t.Fatalf("expected comparison type, got %q", review.QueryType)
	}
	if review.ProviderScope != "" {
		t.Fatalf("expected all-provider scope, got %q", review.ProviderScope)
	}
	if len(review.MentionedProviders) != 3 {
		t.Fatalf("expected 3 mentioned providers, got %#v", review.MentionedProviders)
	}
}

func TestReviewQueryUsesDeterministicModeForConsultComparison(t *testing.T) {
	review := reviewQuery("邊間保險公司有保獸醫 consultation fee？請分公司列出。", "")
	if review.DirectAnswerMode != "deterministic_comparison" {
		t.Fatalf("expected deterministic comparison mode, got %q", review.DirectAnswerMode)
	}
}

func TestReviewAnswerRejectsAbnormalReasoningLeak(t *testing.T) {
	review := reviewQuery("OneDegree 的癌症等候期係幾多日？", "")
	chunks := []indexedChunk{
		{Provider: "one_degree", Source: "one_degree_policy.md", Text: "癌症等候期為180日。"},
	}
	answer := "好的，用户问的是OneDegree的癌症等候期。让我再仔细看证据。"
	got := reviewAnswer(review, "OneDegree 的癌症等候期係幾多日？", answer, chunks, "OneDegree")
	if got == answer {
		t.Fatalf("expected abnormal answer to be rejected")
	}
}

func TestReviewAnswerRejectsIncompleteComparisonCoverage(t *testing.T) {
	review := reviewQuery("比較 OneDegree、Blue Cross 同 Prudential 嘅受傷等候期。", "")
	answer := "Blue Cross 藍十字：受傷等候期為 7 天。"
	got := reviewAnswer(review, "比較 OneDegree、Blue Cross 同 Prudential 嘅受傷等候期。", answer, nil, "All Providers")
	if got == answer {
		t.Fatalf("expected incomplete comparison answer to be rejected")
	}
}
