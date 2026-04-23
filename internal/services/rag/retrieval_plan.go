package rag

import "strings"

type retrievalPlan struct {
	QuestionType    string
	Normalized      string
	RequireWaiting  bool
	RequireCoverage bool
	RequireConsult  bool
	RequireInjury   bool
	RequireCancer   bool
}

func buildRetrievalPlan(query string) retrievalPlan {
	normalized := NormalizeQueryText(query)
	lower := strings.ToLower(normalized)
	questionType := describeQuestionType(normalized)
	return retrievalPlan{
		QuestionType:    questionType,
		Normalized:      normalized,
		RequireWaiting:  questionType == "waiting-period",
		RequireCoverage: questionType == "coverage" || questionType == "chronic-condition",
		RequireConsult:  containsAny(lower, "consult", "consultation", "診症", "诊症", "獸醫", "兽医"),
		RequireInjury:   containsAny(lower, "injury", "injuries", "accident", "受傷", "受伤", "身體損傷", "身体损伤"),
		RequireCancer:   containsAny(lower, "cancer", "癌症", "惡性腫瘤", "恶性肿瘤"),
	}
}

func retrievalAdjustment(plan retrievalPlan, text string) float64 {
	lower := strings.ToLower(text)
	adjustment := 0.0

	if plan.RequireWaiting {
		if containsAny(lower, "waiting period", "waiting periods", "等候期") {
			adjustment += 1.0
		} else {
			adjustment -= 0.9
		}
		if plan.RequireInjury {
			if containsAny(lower, "injury", "injuries", "accident", "受傷", "受伤", "身體損傷", "身体损伤") {
				adjustment += 0.45
			} else {
				adjustment -= 0.35
			}
		}
		if plan.RequireCancer {
			if containsAny(lower, "cancer", "癌症", "惡性腫瘤", "恶性肿瘤") {
				adjustment += 0.45
			} else {
				adjustment -= 0.35
			}
		}
	}

	if plan.RequireCoverage {
		if containsAny(lower, "we will cover", "covered", "cover", "保障", "賠償", "赔偿", "現金保障", "现金保障") {
			adjustment += 0.7
		}
		if containsAny(lower, "waiting period", "waiting periods", "等候期") && !plan.RequireWaiting {
			adjustment -= 0.75
		}
		if plan.RequireConsult {
			if containsAny(lower, "consult", "consultation", "診症", "诊症", "獸醫", "兽医", "prescribed drugs", "處方藥", "处方药") {
				adjustment += 0.55
			} else {
				adjustment -= 0.35
			}
		}
		if plan.RequireCancer {
			if containsAny(lower, "cancer", "癌症", "惡性腫瘤", "恶性肿瘤") {
				adjustment += 0.45
			}
			if containsAny(lower, "waiting period", "等候期") && !containsAny(lower, "保障", "賠償", "赔偿", "現金保障", "现金保障", "索償一次", "一次性") {
				adjustment -= 0.4
			}
		}
	}

	if plan.QuestionType == "comparison" {
		if plan.RequireWaiting && containsAny(lower, "waiting period", "waiting periods", "等候期") {
			adjustment += 0.4
		}
		if plan.RequireConsult && containsAny(lower, "consult", "consultation", "診症", "诊症", "獸醫", "兽医") {
			adjustment += 0.4
		}
	}

	return adjustment
}
