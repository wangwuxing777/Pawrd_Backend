package rag

import (
	"fmt"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/providercatalog"
)

func buildDeterministicComparisonAnswer(question string, chunks []indexedChunk) string {
	plan := buildRetrievalPlan(question)
	language := DetectQueryLanguage(question)
	lines := make([]string, 0, 8)

	if language == "zh" {
		lines = append(lines, "根據檢索到的條款證據：")
	} else {
		lines = append(lines, "Based on the retrieved policy evidence:")
	}

	for _, provider := range orderedProviders(chunks) {
		providerChunks := filterChunksByProvider(chunks, provider)
		if len(providerChunks) == 0 {
			continue
		}
		line, ok := deterministicProviderComparisonLine(plan, provider, providerChunks)
		if !ok {
			continue
		}
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	if len(lines) == 1 {
		return strictFallbackMessage(question)
	}

	if language == "zh" {
		lines = append(lines, "", "建議：如需更精準比較，請繼續問指定保障項目或指定保險公司。")
	} else {
		lines = append(lines, "", "Suggestion: ask about a specific coverage item or provider for a more precise comparison.")
	}
	return strings.Join(lines, "\n")
}

func deterministicProviderComparisonLine(plan retrievalPlan, provider string, chunks []indexedChunk) (string, bool) {
	name := providercatalog.DisplayName(provider)
	if plan.RequireConsult {
		return deterministicConsultLine(name, chunks)
	}
	if plan.RequireWaiting {
		return deterministicWaitingLine(name, chunks, plan), true
	}
	items := buildEvidenceList(plan.Normalized, chunks, 4)
	if len(items) == 0 {
		return "", false
	}
	return fmt.Sprintf("• %s：%s", name, items[0].text), true
}

func deterministicWaitingLine(providerName string, chunks []indexedChunk, plan retrievalPlan) string {
	items := buildEvidenceList(plan.Normalized, chunks, 6)
	for _, item := range items {
		lower := strings.ToLower(item.text)
		if !containsAny(lower, "waiting period", "waiting periods", "等候期") {
			continue
		}
		if plan.RequireInjury && !containsAny(lower, "injury", "accident", "受傷", "受伤", "身體損傷", "身体损伤") {
			continue
		}
		if plan.RequireCancer && !containsAny(lower, "cancer", "癌症", "惡性腫瘤", "恶性肿瘤") {
			continue
		}
		return fmt.Sprintf("• %s：%s", providerName, item.text)
	}
	return fmt.Sprintf("• %s：目前檢索證據不足，未能可靠確認。", providerName)
}

func deterministicConsultLine(providerName string, chunks []indexedChunk) (string, bool) {
	best := ""
	bestScore := -1.0
	for _, chunk := range chunks {
		for _, line := range splitCandidateLines(chunk.Text) {
			lower := strings.ToLower(strings.TrimSpace(line))
			score := 0.0
			if containsAny(lower, "consultation fee", "consultation", "獸醫診症", "兽医诊症", "診症", "诊症", "診金") {
				score += 2.0
			}
			if containsAny(lower, "vet expenses", "獸醫費用", "兽医费用") {
				score += 1.0
			}
			if containsAny(lower, "medical services", "醫療服務費用", "医疗服务费用") &&
				containsAny(lower, "vet", "veterinary", "獸醫", "兽医", "持牌獸醫診所", "持牌兽医诊所") {
				score += 0.75
			}
			if score > bestScore {
				bestScore = score
				best = strings.TrimSpace(line)
			}
		}
	}
	if bestScore <= 0 {
		return "", false
	}
	return fmt.Sprintf("• %s：%s", providerName, best), true
}
