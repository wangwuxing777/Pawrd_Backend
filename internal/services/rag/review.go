package rag

import (
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/providercatalog"
)

type queryReview struct {
	Route              string
	QueryType          string
	ProviderScope      string
	MentionedProviders []string
	AnswerChecks       []string
	FallbackType       string
	DirectAnswerMode   string
}

func reviewQuery(query, requestedProvider string) queryReview {
	normalizedProvider := providercatalog.NormalizeProviderID(requestedProvider)
	queryType := describeQuestionType(query)
	mentionedProviders := providercatalog.DetectProviders(query)

	review := queryReview{
		Route:              "rag",
		QueryType:          queryType,
		ProviderScope:      normalizedProvider,
		MentionedProviders: mentionedProviders,
		AnswerChecks:       buildAnswerChecks(queryType, mentionedProviders),
		FallbackType:       "none",
		DirectAnswerMode:   "model",
	}

	if isGreetingOrSmallTalk(query) {
		review.Route = "fallback"
		review.QueryType = "small_talk"
		review.ProviderScope = ""
		review.AnswerChecks = nil
		review.FallbackType = "greeting"
		return review
	}

	if queryType == "comparison" || len(mentionedProviders) > 1 {
		review.QueryType = "comparison"
		review.ProviderScope = ""
	}
	if review.QueryType == "comparison" && isConsultFeeQuery(query) {
		review.DirectAnswerMode = "deterministic_comparison"
	}

	if review.ProviderScope == "" && len(mentionedProviders) == 1 && queryType != "comparison" {
		review.ProviderScope = mentionedProviders[0]
	}

	return review
}

func buildAnswerChecks(queryType string, mentionedProviders []string) []string {
	checks := []string{"abnormal_output", "non_empty_answer"}
	switch queryType {
	case "comparison":
		checks = append(checks, "provider_coverage", "comparison_structure")
	case "waiting-period":
		checks = append(checks, "fact_specificity")
	case "coverage", "chronic-condition":
		checks = append(checks, "intent_match", "exception_handling")
	default:
		checks = append(checks, "intent_match")
	}
	if len(mentionedProviders) > 1 {
		checks = append(checks, "provider_coverage")
	}
	return dedupeStrings(checks)
}

func isConsultFeeQuery(query string) bool {
	lower := strings.ToLower(NormalizeQueryText(query))
	return containsAny(lower, "consult", "consultation", "診症", "诊症", "獸醫", "兽医") &&
		containsAny(lower, "fee", "費", "费", "邊間", "边间", "compare", "比較", "比较", "分公司列出")
}

func reviewAnswer(plan queryReview, question, answer string, chunks []indexedChunk, activeProviderName string) string {
	cleaned := CleanModelOutput(answer)
	if cleaned == "" {
		return fallbackAnswerForPlan(plan, question, chunks, activeProviderName)
	}
	if hasAbnormalAnswerOutput(cleaned) {
		return fallbackAnswerForPlan(plan, question, chunks, activeProviderName)
	}
	if plan.QueryType == "comparison" && missingMentionedProviders(cleaned, plan.MentionedProviders) {
		return fallbackAnswerForPlan(plan, question, chunks, activeProviderName)
	}
	return cleaned
}

func fallbackAnswerForPlan(plan queryReview, question string, chunks []indexedChunk, activeProviderName string) string {
	if plan.QueryType == "comparison" {
		return strictFallbackMessage(question)
	}
	return fallbackAnswer(question, chunks, activeProviderName == "All Providers")
}

func hasAbnormalAnswerOutput(answer string) bool {
	lower := strings.ToLower(answer)
	patterns := []string{
		"好的，用户问的是",
		"让我再仔细看",
		"根据提供的政策证据，我需要仔细分析",
		"首先，用户的问题是",
		"i need to carefully analyze",
		"let me carefully analyze",
		"the user is asking",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func missingMentionedProviders(answer string, providers []string) bool {
	if len(providers) <= 1 {
		return false
	}
	lower := strings.ToLower(answer)
	for _, provider := range providers {
		name := strings.ToLower(providercatalog.DisplayName(provider))
		if strings.Contains(lower, name) {
			continue
		}
		if strings.Contains(lower, strings.ToLower(provider)) {
			continue
		}
		return true
	}
	return false
}

func dedupeStrings(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
