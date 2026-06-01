package raggo

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	waitingPeriodCancerDaysRe  = regexp.MustCompile(`(?i)(?:cancer|malignant)[^.\n;]{0,48}?(?:waiting period|等候期)[^0-9\n]{0,18}?(\d+)\s*(?:days?|天|日)`)
	waitingPeriodInjuryDaysRe  = regexp.MustCompile(`(?i)(?:injury|injuries|bodily injury|accident|受傷|受伤)[^.\n;]{0,48}?(?:waiting period|等候期)[^0-9\n]{0,18}?(\d+)\s*(?:days?|天|日)`)
	waitingPeriodIllnessDaysRe = regexp.MustCompile(`(?i)(?:illness|disease|疾病)[^.\n;]{0,48}?(?:waiting period|等候期)[^0-9\n]{0,18}?(\d+)\s*(?:days?|天|日)`)
	waitingPeriodGeneralDaysRe = regexp.MustCompile(`(?i)(?:waiting period|等候期)[^0-9\n]{0,40}?(\d+)\s*(?:days?|天|日)`)
	simpleDaysRe               = regexp.MustCompile(`(?i)(\d+)\s*(?:days?|天|日)`)
	planLineEnRe               = regexp.MustCompile(`(?i)\bplan\s*([A-Z0-9]+)\s*[:：]?\s*(.*)`)
	planLineZhRe               = regexp.MustCompile(`計劃\s*([A-Z0-9]+)\s*[:：]?\s*(.*)`)
)

var providerDisplayNames = map[string]string{
	"one_degree": "OneDegree",
	"bluecross":  "Blue Cross",
	"prudential": "Prudential",
	"msig":       "MSIG",
}

var providerAliases = map[string][]string{
	"one_degree": {"one degree", "onedegree", "one_degree"},
	"bluecross":  {"blue cross", "bluecross", "藍十字", "蓝十字"},
	"prudential": {"prudential", "保誠", "保诚"},
	"msig":       {"msig"},
}

type QueryIntent struct {
	RawQuestion      string
	Normalized       string
	WantsComparison  bool
	WantsWaiting     bool
	AsksDefinition   bool
	WantsConsult     bool
	WantsCoverage    bool
	AsksLimit        bool
	WantsCancer      bool
	WantsInjury      bool
	TargetProviders  []string
	ProviderOverride string
}

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

type waitingPeriodFact struct {
	Provider        string
	Clauses         string
	SourceName      string
	CancerDays      *int
	InjuryDays      *int
	IllnessDays     *int
	GeneralDays     *int
	NoWaitingPeriod bool
}

type consultCoverageFact struct {
	Provider          string
	Clauses           string
	SourceName        string
	ConsultationLabel string
}

type planLimits map[string]map[string]string

type consultLimitFact struct {
	Provider          string
	Clauses           string
	SourceName        string
	ConsultationLabel string
	PlanLimits        planLimits
	HasExplicitLimit  bool
}

type benefitLimitFact struct {
	Provider         string
	Clauses          string
	SourceName       string
	BenefitLabel     string
	PlanLimits       planLimits
	HasExplicitLimit bool
}

func AnswerQuery(cfg Config, question, provider, language string, maxSources int) AnswerResult {
	started := time.Now()
	normalizedQ := strings.TrimSpace(question)
	intent := detectQueryIntent(normalizedQ, provider)

	chunks, err := LoadChunks(cfg)
	if err != nil {
		return AnswerResult{
			Question:       normalizedQ,
			Provider:       provider,
			Language:       language,
			Intent:         formatIntent(intent),
			Answer:         "RAG corpus loading failed in Go runtime: " + err.Error(),
			AnswerMode:     "go_error",
			Disclaimer:     "仅供参考，不保证 100% 准确、完整或最新。最终以保险公司官网、正式保单、承保表、批单及最新书面说明为准。",
			Sources:        nil,
			ProcessingMS:   time.Since(started).Milliseconds(),
			Implementation: "go_rag_translation_v2",
		}
	}

	candidates := rankCandidates(chunks, intent, provider, language, maxSources)
	sources := make([]Source, 0, len(candidates))
	for _, c := range candidates {
		sources = append(sources, buildSourcePayload(c.chunk, c.score))
	}
	if maxSources > 0 && len(sources) > maxSources {
		sources = sources[:maxSources]
	}

	answer, mode, structured := buildDeterministicAnswer(intent, sources)
	if answer == "" {
		answer = "Go translation runtime retrieved relevant policy evidence but deterministic extraction is not ready for this exact query shape yet."
		mode = "go_retrieval_fallback"
	}

	return AnswerResult{
		Question:       normalizedQ,
		Provider:       provider,
		Language:       language,
		Intent:         formatIntent(intent),
		Answer:         answer,
		AnswerMode:     mode,
		Structured:     structured,
		Disclaimer:     "仅供参考，不保证 100% 准确、完整或最新。最终以保险公司官网、正式保单、承保表、批单及最新书面说明为准。",
		Sources:        sources,
		ProcessingMS:   time.Since(started).Milliseconds(),
		Implementation: "go_rag_translation_v2",
	}
}

func rankCandidates(chunks []Chunk, intent QueryIntent, provider, language string, maxSources int) []rankedChunk {
	qTokens := tokenize(intent.RawQuestion)
	out := make([]rankedChunk, 0, 64)
	for _, ch := range chunks {
		if provider != "" && ch.Metadata["provider"] != provider {
			continue
		}
		if language != "" && ch.Metadata["language"] != language {
			continue
		}
		if provider == "" && intent.WantsComparison && len(intent.TargetProviders) > 0 {
			if !contains(intent.TargetProviders, ch.Metadata["provider"]) {
				continue
			}
		}
		text := strings.ToLower(ch.Text)
		score := lexicalScore(text, qTokens)
		score += metadataBonus(ch, intent)
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

	if intent.WantsConsult {
		out = reorderByScore(out, func(ch Chunk) float64 {
			return consultAnswerScore(intent, ch)
		})
	}
	if intent.AsksLimit && !intent.WantsConsult {
		out = reorderByScore(out, func(ch Chunk) float64 {
			return genericLimitAnswerScore(intent, ch)
		})
	}
	if intent.WantsWaiting {
		out = reorderByScore(out, func(ch Chunk) float64 {
			return waitingPeriodAnswerScore(intent, ch)
		})
	}

	if intent.WantsComparison && provider == "" {
		out = ensureComparisonProviderCoverage(out, intent.TargetProviders)
		out = diversifyByProvider(out)
	}

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

func ensureComparisonProviderCoverage(items []rankedChunk, targetProviders []string) []rankedChunk {
	if len(items) == 0 || len(targetProviders) == 0 {
		return items
	}

	bestByProvider := map[string]rankedChunk{}
	for _, item := range items {
		p := item.chunk.Metadata["provider"]
		if p == "" {
			continue
		}
		if _, exists := bestByProvider[p]; !exists {
			bestByProvider[p] = item
		}
	}

	prepend := make([]rankedChunk, 0, len(targetProviders))
	seen := map[string]bool{}
	for _, p := range targetProviders {
		if best, ok := bestByProvider[p]; ok {
			prepend = append(prepend, best)
			seen[p+"|"+best.chunk.Metadata["source_name"]+"|"+best.chunk.Metadata["section_path"]] = true
		}
	}
	if len(prepend) == 0 {
		return items
	}

	out := make([]rankedChunk, 0, len(items))
	out = append(out, prepend...)
	for _, item := range items {
		key := item.chunk.Metadata["provider"] + "|" + item.chunk.Metadata["source_name"] + "|" + item.chunk.Metadata["section_path"]
		if seen[key] {
			continue
		}
		out = append(out, item)
	}
	return out
}

func tokenize(question string) []string {
	l := strings.ToLower(question)
	repl := strings.NewReplacer(",", " ", ".", " ", "?", " ", "!", " ", "，", " ", "。", " ", "？", " ", "：", " ", ":", " ")
	l = repl.Replace(l)
	parts := strings.Fields(l)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len([]rune(p)) <= 1 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func lexicalScore(text string, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	score := 0.0
	for _, t := range tokens {
		if strings.Contains(text, t) {
			score += 1
		}
	}
	return score
}

func metadataBonus(ch Chunk, intent QueryIntent) float64 {
	lq := intent.Normalized
	text := strings.ToLower(ch.Text)
	tags := strings.ToLower(ch.Metadata["topic_tags"])
	unit := strings.ToLower(ch.Metadata["unit_types"])
	section := strings.ToLower(ch.Metadata["section_path"])
	bonus := 0.0

	if intent.WantsWaiting {
		if strings.Contains(tags, "waiting_period") || strings.Contains(unit, "waiting_period") || strings.Contains(section, "waiting period") || strings.Contains(section, "等候期") {
			bonus += 3
		}
		if containsAny(text, "no waiting period", "不設等候期", "不设等候期", "days", "天", "日") {
			bonus += 1.5
		}
	}
	if intent.WantsConsult {
		if strings.Contains(tags, "consult") || containsAny(section, "consultation", "診症", "诊症", "診金") {
			bonus += 3
		}
		if containsDirectConsultReference(text) {
			bonus += 2
		}
	}
	if intent.AsksLimit {
		if hasExplicitLimitPattern(ch.Text) {
			bonus += 2.5
		}
		if strings.Contains(tags, "limit") || containsAny(section, "maximum", "limits", "上限", "最高賠償額", "最高赔偿额") {
			bonus += 2
		}
		if extracted := extractPlanLimits(ch.Text); len(extracted) > 0 {
			bonus += 2
		}
	}

	if intent.WantsComparison && len(intent.TargetProviders) > 0 {
		if contains(intent.TargetProviders, ch.Metadata["provider"]) {
			bonus += 1.2
		}
	}

	if containsAny(lq, "room and board", "住院", "hospital") && containsAny(text, "room and board", "confinement") {
		bonus += 1.2
	}

	return bonus
}

func buildSourcePayload(ch Chunk, score float64) Source {
	snippet := strings.TrimSpace(ch.Text)
	if len(snippet) > 800 {
		snippet = snippet[:800]
	}
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

func buildDeterministicAnswer(intent QueryIntent, sources []Source) (string, string, map[string]any) {
	if len(sources) == 0 {
		return "No reliable evidence found in current Go runtime retrieval for this query.", "go_no_evidence", nil
	}

	if intent.WantsWaiting {
		if answer, mode, payload := buildDeterministicWaitingPeriodAnswer(intent, sources); answer != "" {
			return answer, mode, payload
		}
	}
	if intent.WantsConsult && intent.AsksLimit {
		if answer, mode, payload := buildDeterministicConsultLimitAnswer(intent, sources); answer != "" {
			return answer, mode, payload
		}
	}
	if intent.AsksLimit && !intent.WantsConsult {
		if answer, mode, payload := buildDeterministicBenefitLimitAnswer(intent, sources); answer != "" {
			return answer, mode, payload
		}
	}
	if intent.WantsConsult && (intent.WantsCoverage || !intent.AsksLimit) {
		if answer, mode, payload := buildDeterministicConsultCoverageAnswer(intent, sources); answer != "" {
			return answer, mode, payload
		}
	}
	return "", "", nil
}

func buildDeterministicWaitingPeriodAnswer(intent QueryIntent, sources []Source) (string, string, map[string]any) {
	facts := collectWaitingPeriodFacts(sources)
	if len(facts) == 0 {
		return "", "", nil
	}
	preferZH := questionPrefersZH(intent.RawQuestion)

	if intent.AsksDefinition && intent.ProviderOverride == "" && !intent.WantsComparison {
		byProvider := map[string]waitingPeriodFact{}
		for _, fact := range facts {
			if _, exists := byProvider[fact.Provider]; !exists {
				byProvider[fact.Provider] = fact
			}
		}
		if len(byProvider) > 0 {
			providers := make([]string, 0, len(byProvider))
			for p := range byProvider {
				providers = append(providers, p)
			}
			sort.Strings(providers)

			lines := make([]string, 0, len(providers))
			items := make([]map[string]any, 0, len(providers))
			for _, p := range providers {
				f := byProvider[p]
				summary := waitingPeriodEvidenceSummary(f, preferZH)
				if summary == "" {
					continue
				}
				if preferZH {
					lines = append(lines, "- "+providerDisplay(p)+"："+summary+formatClauseSuffix(f.Clauses, true))
				} else {
					lines = append(lines, "- "+providerDisplay(p)+": "+summary+formatClauseSuffix(f.Clauses, false))
				}
				items = append(items, map[string]any{
					"provider":    p,
					"clauses":     f.Clauses,
					"source_name": f.SourceName,
					"illness_days": func() any {
						if f.IllnessDays == nil {
							return nil
						}
						return *f.IllnessDays
					}(),
					"cancer_days": func() any {
						if f.CancerDays == nil {
							return nil
						}
						return *f.CancerDays
					}(),
					"injury_days": func() any {
						if f.InjuryDays == nil {
							return nil
						}
						return *f.InjuryDays
					}(),
					"general_days": func() any {
						if f.GeneralDays == nil {
							return nil
						}
						return *f.GeneralDays
					}(),
					"no_waiting_period": f.NoWaitingPeriod,
				})
			}
			if len(lines) > 0 {
				answer := "In the retrieved policy clauses, a waiting period is the time that must pass before the listed condition can be claimed. The exact trigger and duration vary by provider:\n" + strings.Join(lines, "\n")
				if preferZH {
					answer = "根據已檢索到的保單條款，等候期是指保單生效後，需要先過指定日數，相關疾病或情況才可索償；實際日數會因保險公司和保障項目而異：\n" + strings.Join(lines, "\n")
				}
				return answer, "deterministic_waiting_period_single", map[string]any{
					"type":  "waiting_period_single",
					"items": items,
				}
			}
		}
	}

	if intent.WantsComparison {
		byProvider := map[string]waitingPeriodFact{}
		for _, fact := range facts {
			if _, exists := byProvider[fact.Provider]; !exists {
				byProvider[fact.Provider] = fact
			}
		}
		if len(byProvider) >= 2 {
			providers := make([]string, 0, len(byProvider))
			for p := range byProvider {
				providers = append(providers, p)
			}
			sort.Strings(providers)
			lines := make([]string, 0, len(providers))
			items := make([]map[string]any, 0, len(providers))
			for _, p := range providers {
				f := byProvider[p]
				label, days := bestWaitingPeriodValue(intent, f)
				line := formatWaitingPeriodLine(providerDisplay(p), label, days, f, preferZH)
				lines = append(lines, line)
				items = append(items, map[string]any{
					"provider":          p,
					"clauses":           f.Clauses,
					"source_name":       f.SourceName,
					"label":             label,
					"days":              days,
					"no_waiting_period": f.NoWaitingPeriod,
				})
			}
			answer := "Waiting period comparison:\n" + strings.Join(lines, "\n")
			if preferZH {
				answer = "等候期比較：\n" + strings.Join(lines, "\n")
			}
			return answer, "deterministic_waiting_period_comparison", map[string]any{
				"type":  "waiting_period_comparison",
				"items": items,
			}
		}
	}

	first := facts[0]
	label, days := bestWaitingPeriodValue(intent, first)
	answer := formatSingleWaitingPeriodLine(providerDisplay(first.Provider), label, days, first, preferZH)
	return answer, "deterministic_waiting_period_single", map[string]any{
		"type":              "waiting_period_single",
		"provider":          first.Provider,
		"clauses":           first.Clauses,
		"source_name":       first.SourceName,
		"label":             label,
		"days":              days,
		"no_waiting_period": first.NoWaitingPeriod,
	}
}

func buildDeterministicConsultCoverageAnswer(intent QueryIntent, sources []Source) (string, string, map[string]any) {
	fact, ok := extractConsultCoverageFact(sources)
	if !ok {
		return "", "", nil
	}
	preferZH := questionPrefersZH(intent.RawQuestion)
	provider := providerDisplay(fact.Provider)
	answer := provider + " covers " + fact.ConsultationLabel + formatClauseSuffix(fact.Clauses, false)
	if preferZH {
		answer = provider + " 的保單涵蓋" + fact.ConsultationLabel + formatClauseSuffix(fact.Clauses, true)
	}
	return answer, "deterministic_consult_coverage_single", map[string]any{
		"type":               "consult_coverage_single",
		"provider":           fact.Provider,
		"source_name":        fact.SourceName,
		"clauses":            fact.Clauses,
		"consultation_label": fact.ConsultationLabel,
	}
}

func buildDeterministicConsultLimitAnswer(intent QueryIntent, sources []Source) (string, string, map[string]any) {
	facts := collectConsultLimitFacts(intent, sources)
	if len(facts) == 0 {
		return "", "", nil
	}
	preferZH := questionPrefersZH(intent.RawQuestion)

	if intent.WantsComparison {
		byProvider := map[string]consultLimitFact{}
		for _, f := range facts {
			if _, exists := byProvider[f.Provider]; !exists {
				byProvider[f.Provider] = f
			}
		}
		if len(byProvider) >= 2 {
			providers := make([]string, 0, len(byProvider))
			for p := range byProvider {
				providers = append(providers, p)
			}
			sort.Strings(providers)
			lines := make([]string, 0, len(providers))
			items := make([]map[string]any, 0, len(providers))
			for _, p := range providers {
				f := byProvider[p]
				display := providerDisplay(p)
				if f.HasExplicitLimit {
					if preferZH {
						lines = append(lines, "- "+display+"："+f.ConsultationLabel+"最高賠償額為 "+formatPlanLimitsZh(f.PlanLimits))
					} else {
						lines = append(lines, "- "+display+": "+f.ConsultationLabel+" limit is "+formatPlanLimitsEn(f.PlanLimits))
					}
				} else if preferZH {
					lines = append(lines, "- "+display+"：已檢索到"+f.ConsultationLabel+"條款，但目前證據內沒有明確的最高賠償額數字")
				} else {
					lines = append(lines, "- "+display+": the "+f.ConsultationLabel+" clause was retrieved, but no explicit limit amount was found")
				}
				items = append(items, map[string]any{
					"provider":           p,
					"clauses":            f.Clauses,
					"source_name":        f.SourceName,
					"consultation_label": f.ConsultationLabel,
					"plan_limits":        f.PlanLimits,
					"has_explicit_limit": f.HasExplicitLimit,
				})
			}
			answer := "Consultation limit comparison:\n" + strings.Join(lines, "\n")
			if preferZH {
				answer = "診症保障限額比較：\n" + strings.Join(lines, "\n")
			}
			return answer, "deterministic_consult_limit_comparison", map[string]any{
				"type":  "consult_limit_comparison",
				"items": items,
			}
		}
	}

	fact := facts[0]
	provider := providerDisplay(fact.Provider)
	if !fact.HasExplicitLimit {
		if preferZH {
			return provider + " 的" + fact.ConsultationLabel + "條款已檢索到，但目前證據內沒有明確的最高賠償額數字" + formatClauseSuffix(fact.Clauses, true),
				"deterministic_consult_limit_single",
				map[string]any{
					"type":               "consult_limit_single",
					"provider":           fact.Provider,
					"source_name":        fact.SourceName,
					"clauses":            fact.Clauses,
					"consultation_label": fact.ConsultationLabel,
					"plan_limits":        fact.PlanLimits,
					"has_explicit_limit": false,
				}
		}
		return provider + " " + fact.ConsultationLabel + " clause was retrieved, but no explicit limit amount was found in the current evidence" + formatClauseSuffix(fact.Clauses, false),
			"deterministic_consult_limit_single",
			map[string]any{
				"type":               "consult_limit_single",
				"provider":           fact.Provider,
				"source_name":        fact.SourceName,
				"clauses":            fact.Clauses,
				"consultation_label": fact.ConsultationLabel,
				"plan_limits":        fact.PlanLimits,
				"has_explicit_limit": false,
			}
	}

	answer := provider + " " + fact.ConsultationLabel + " limit is " + formatPlanLimitsEn(fact.PlanLimits) + formatClauseSuffix(fact.Clauses, false)
	if preferZH {
		answer = provider + " 的" + fact.ConsultationLabel + "最高賠償額為 " + formatPlanLimitsZh(fact.PlanLimits) + formatClauseSuffix(fact.Clauses, true)
	}
	return answer, "deterministic_consult_limit_single", map[string]any{
		"type":               "consult_limit_single",
		"provider":           fact.Provider,
		"source_name":        fact.SourceName,
		"clauses":            fact.Clauses,
		"consultation_label": fact.ConsultationLabel,
		"plan_limits":        fact.PlanLimits,
		"has_explicit_limit": true,
	}
}

func buildDeterministicBenefitLimitAnswer(intent QueryIntent, sources []Source) (string, string, map[string]any) {
	facts := collectBenefitLimitFacts(intent, sources)
	if len(facts) == 0 {
		return "", "", nil
	}
	preferZH := questionPrefersZH(intent.RawQuestion)

	if intent.WantsComparison {
		byProvider := map[string]benefitLimitFact{}
		for _, f := range facts {
			if _, exists := byProvider[f.Provider]; !exists {
				byProvider[f.Provider] = f
			}
		}
		if len(byProvider) >= 2 {
			providers := make([]string, 0, len(byProvider))
			for p := range byProvider {
				providers = append(providers, p)
			}
			sort.Strings(providers)
			lines := make([]string, 0, len(providers))
			items := make([]map[string]any, 0, len(providers))
			for _, p := range providers {
				f := byProvider[p]
				display := providerDisplay(p)
				if f.HasExplicitLimit {
					if preferZH {
						lines = append(lines, "- "+display+"："+f.BenefitLabel+"最高賠償額為 "+formatPlanLimitsZh(f.PlanLimits))
					} else {
						lines = append(lines, "- "+display+": "+f.BenefitLabel+" limit is "+formatPlanLimitsEn(f.PlanLimits))
					}
				} else if preferZH {
					lines = append(lines, "- "+display+"：已檢索到"+f.BenefitLabel+"條款，但目前證據內沒有明確的最高賠償額數字")
				} else {
					lines = append(lines, "- "+display+": the "+f.BenefitLabel+" clause was retrieved, but no explicit limit amount was found")
				}
				items = append(items, map[string]any{
					"provider":           p,
					"clauses":            f.Clauses,
					"source_name":        f.SourceName,
					"benefit_label":      f.BenefitLabel,
					"plan_limits":        f.PlanLimits,
					"has_explicit_limit": f.HasExplicitLimit,
				})
			}
			answer := "Benefit limit comparison:\n" + strings.Join(lines, "\n")
			if preferZH {
				answer = "保障限額比較：\n" + strings.Join(lines, "\n")
			}
			return answer, "deterministic_benefit_limit_comparison", map[string]any{
				"type":  "benefit_limit_comparison",
				"items": items,
			}
		}
	}

	fact := facts[0]
	provider := providerDisplay(fact.Provider)
	if !fact.HasExplicitLimit {
		if preferZH {
			return provider + " 的" + fact.BenefitLabel + "條款已檢索到，但目前證據內沒有明確的最高賠償額數字" + formatClauseSuffix(fact.Clauses, true),
				"deterministic_benefit_limit_single",
				map[string]any{
					"type":               "benefit_limit_single",
					"provider":           fact.Provider,
					"source_name":        fact.SourceName,
					"clauses":            fact.Clauses,
					"benefit_label":      fact.BenefitLabel,
					"plan_limits":        fact.PlanLimits,
					"has_explicit_limit": false,
				}
		}
		return provider + " " + fact.BenefitLabel + " clause was retrieved, but no explicit limit amount was found in the current evidence" + formatClauseSuffix(fact.Clauses, false),
			"deterministic_benefit_limit_single",
			map[string]any{
				"type":               "benefit_limit_single",
				"provider":           fact.Provider,
				"source_name":        fact.SourceName,
				"clauses":            fact.Clauses,
				"benefit_label":      fact.BenefitLabel,
				"plan_limits":        fact.PlanLimits,
				"has_explicit_limit": false,
			}
	}

	answer := provider + " " + fact.BenefitLabel + " limit is " + formatPlanLimitsEn(fact.PlanLimits) + formatClauseSuffix(fact.Clauses, false)
	if preferZH {
		answer = provider + " 的" + fact.BenefitLabel + "最高賠償額為 " + formatPlanLimitsZh(fact.PlanLimits) + formatClauseSuffix(fact.Clauses, true)
	}
	return answer, "deterministic_benefit_limit_single", map[string]any{
		"type":               "benefit_limit_single",
		"provider":           fact.Provider,
		"source_name":        fact.SourceName,
		"clauses":            fact.Clauses,
		"benefit_label":      fact.BenefitLabel,
		"plan_limits":        fact.PlanLimits,
		"has_explicit_limit": true,
	}
}

func collectWaitingPeriodFacts(sources []Source) []waitingPeriodFact {
	out := make([]waitingPeriodFact, 0, 8)
	for _, src := range sources {
		if fact, ok := extractWaitingPeriodFact(src); ok {
			out = append(out, fact)
		}
	}
	return out
}

func extractWaitingPeriodFact(src Source) (waitingPeriodFact, bool) {
	lower := strings.ToLower(src.Snippet)
	if !containsAny(lower, "waiting period", "等候期", "等待期", "不設等候期", "不设等候期") &&
		!containsAny(strings.ToLower(src.TopicTags), "waiting_period") &&
		!containsAny(strings.ToLower(src.UnitTypes), "waiting_period", "definition") {
		return waitingPeriodFact{}, false
	}
	cancer := extractWaitingPeriodDays(src.Snippet, "cancer")
	injury := extractWaitingPeriodDays(src.Snippet, "injury")
	illness := extractWaitingPeriodDays(src.Snippet, "illness")
	general := extractWaitingPeriodDays(src.Snippet, "general")
	noWaiting := containsAny(lower, "no waiting period", "不設等候期", "不设等候期")
	if !noWaiting && cancer == nil && injury == nil && illness == nil && general == nil {
		return waitingPeriodFact{}, false
	}
	return waitingPeriodFact{
		Provider:        src.Provider,
		Clauses:         src.Clauses,
		SourceName:      src.SourceName,
		CancerDays:      cancer,
		InjuryDays:      injury,
		IllnessDays:     illness,
		GeneralDays:     general,
		NoWaitingPeriod: noWaiting,
	}, true
}

func extractConsultCoverageFact(sources []Source) (consultCoverageFact, bool) {
	for _, src := range sources {
		if consultAnswerScore(detectQueryIntent("consult coverage", ""), chunkFromSource(src)) <= 0 {
			continue
		}
		lower := strings.ToLower(src.Snippet)
		if containsDirectConsultReference(lower) || containsAny(strings.ToLower(src.TopicTags), "consult") {
			preferZH := src.Language == "zh"
			return consultCoverageFact{
				Provider:          src.Provider,
				Clauses:           src.Clauses,
				SourceName:        src.SourceName,
				ConsultationLabel: consultationLabelForText(src.Snippet, preferZH),
			}, true
		}
	}
	return consultCoverageFact{}, false
}

func collectConsultLimitFacts(intent QueryIntent, sources []Source) []consultLimitFact {
	out := make([]consultLimitFact, 0, 6)
	for _, src := range sources {
		if consultAnswerScore(intent, chunkFromSource(src)) <= 0 {
			continue
		}
		limits := extractPlanLimits(src.Snippet)
		out = append(out, consultLimitFact{
			Provider:          src.Provider,
			Clauses:           src.Clauses,
			SourceName:        src.SourceName,
			ConsultationLabel: consultationLabelForText(src.Snippet, questionPrefersZH(intent.RawQuestion)),
			PlanLimits:        limits,
			HasExplicitLimit:  len(limits) > 0,
		})
	}
	return out
}

func collectBenefitLimitFacts(intent QueryIntent, sources []Source) []benefitLimitFact {
	out := make([]benefitLimitFact, 0, 6)
	for _, src := range sources {
		if genericLimitAnswerScore(intent, chunkFromSource(src)) <= 0 {
			continue
		}
		limits := extractPlanLimits(src.Snippet)
		if len(limits) == 0 && !hasExplicitLimitPattern(src.Snippet) {
			continue
		}
		out = append(out, benefitLimitFact{
			Provider:         src.Provider,
			Clauses:          src.Clauses,
			SourceName:       src.SourceName,
			BenefitLabel:     inferBenefitLabel(src.Snippet, questionPrefersZH(intent.RawQuestion)),
			PlanLimits:       limits,
			HasExplicitLimit: len(limits) > 0,
		})
	}
	return out
}

func extractWaitingPeriodDays(text, category string) *int {
	var re *regexp.Regexp
	switch category {
	case "cancer":
		re = waitingPeriodCancerDaysRe
	case "injury":
		re = waitingPeriodInjuryDaysRe
	case "illness":
		re = waitingPeriodIllnessDaysRe
	default:
		re = waitingPeriodGeneralDaysRe
	}
	match := re.FindStringSubmatch(text)
	if len(match) < 2 {
		return extractWaitingPeriodDaysFallback(text, category)
	}
	v, err := strconv.Atoi(strings.TrimSpace(match[1]))
	if err != nil {
		return extractWaitingPeriodDaysFallback(text, category)
	}
	return &v
}

func extractWaitingPeriodDaysFallback(text, category string) *int {
	lines := strings.Split(text, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		switch category {
		case "cancer":
			if !containsAny(lower, "cancer", "malignant", "癌症", "惡性腫瘤", "恶性肿瘤") {
				continue
			}
		case "injury":
			if !containsAny(lower, "injury", "bodily injury", "accident", "受傷", "受伤") {
				continue
			}
		case "illness":
			if !containsAny(lower, "illness", "disease", "疾病") {
				continue
			}
		default:
			if !containsAny(lower, "waiting period", "等候期", "等待期") {
				continue
			}
		}
		match := simpleDaysRe.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(match[1]))
		if err == nil {
			return &v
		}
	}
	return nil
}

func bestWaitingPeriodValue(intent QueryIntent, fact waitingPeriodFact) (string, *int) {
	if intent.WantsCancer && fact.CancerDays != nil {
		return "cancer waiting period", fact.CancerDays
	}
	if intent.WantsInjury && fact.InjuryDays != nil {
		return "injury waiting period", fact.InjuryDays
	}
	if fact.IllnessDays != nil {
		return "illness waiting period", fact.IllnessDays
	}
	if fact.GeneralDays != nil {
		return "waiting period", fact.GeneralDays
	}
	if fact.CancerDays != nil {
		return "cancer waiting period", fact.CancerDays
	}
	if fact.InjuryDays != nil {
		return "injury waiting period", fact.InjuryDays
	}
	return "waiting period", nil
}

func hasExplicitLimitPattern(text string) bool {
	l := strings.ToLower(text)
	return containsAny(l, "hk$", "港幣", "per visit", "per day", "per year", "每次", "每天", "每年")
}

func extractPlanLimits(text string) planLimits {
	limits := planLimits{}
	lines := strings.Split(strings.ReplaceAll(text, "：", ":"), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		var plan string
		var rest string
		if m := planLineEnRe.FindStringSubmatch(line); len(m) == 3 {
			plan = strings.ToUpper(strings.TrimSpace(m[1]))
			rest = strings.TrimSpace(m[2])
		} else if m := planLineZhRe.FindStringSubmatch(line); len(m) == 3 {
			plan = strings.ToUpper(strings.TrimSpace(m[1]))
			rest = strings.TrimSpace(m[2])
		} else {
			continue
		}
		if rest == "" {
			continue
		}
		parsed := parsePlanLimitLine(rest)
		if len(parsed) == 0 {
			continue
		}
		limits[plan] = parsed
	}
	return limits
}

func parsePlanLimitLine(text string) map[string]string {
	lower := strings.ToLower(text)
	result := map[string]string{}

	enAmounts := regexp.MustCompile(`(?i)HK\$\s*([0-9,]+)`).FindAllStringSubmatch(text, -1)
	zhAmounts := regexp.MustCompile(`港幣\s*([0-9,]+)`).FindAllStringSubmatch(text, -1)

	annualZh := regexp.MustCompile(`每年[^港幣]{0,10}港幣\s*([0-9,]+)`).FindStringSubmatch(text)
	if len(annualZh) == 2 {
		result["annual_limit"] = "港幣" + strings.TrimSpace(annualZh[1])
	}
	annualEn := regexp.MustCompile(`(?i)HK\$\s*([0-9,]+)[^.\n;]{0,20}per\s*(?:policy\s*)?year`).FindStringSubmatch(text)
	if len(annualEn) == 2 {
		result["annual_limit"] = "HK$" + strings.TrimSpace(annualEn[1])
	}
	if _, ok := result["annual_limit"]; !ok {
		if len(enAmounts) > 0 && containsAny(lower, "per year", "policy year", "annual") {
			result["annual_limit"] = "HK$" + strings.TrimSpace(enAmounts[0][1])
		} else if len(zhAmounts) > 0 && containsAny(lower, "每年", "全年") {
			result["annual_limit"] = "港幣" + strings.TrimSpace(zhAmounts[0][1])
		}
	}

	visitZh := regexp.MustCompile(`每次[^港幣]{0,10}港幣\s*([0-9,]+)`).FindStringSubmatch(text)
	if len(visitZh) == 2 {
		result["per_visit_limit"] = "港幣" + strings.TrimSpace(visitZh[1])
	}
	visitEn := regexp.MustCompile(`(?i)HK\$\s*([0-9,]+)[^.\n;]{0,20}per\s*visit`).FindStringSubmatch(text)
	if len(visitEn) == 2 {
		result["per_visit_limit"] = "HK$" + strings.TrimSpace(visitEn[1])
	}

	dayZh := regexp.MustCompile(`每天[^港幣]{0,10}港幣\s*([0-9,]+)`).FindStringSubmatch(text)
	if len(dayZh) == 2 {
		result["per_day_limit"] = "港幣" + strings.TrimSpace(dayZh[1])
	}
	dayEn := regexp.MustCompile(`(?i)HK\$\s*([0-9,]+)[^.\n;]{0,20}per\s*day`).FindStringSubmatch(text)
	if len(dayEn) == 2 {
		result["per_day_limit"] = "HK$" + strings.TrimSpace(dayEn[1])
	}

	return result
}

func consultationLabelForText(text string, preferZH bool) string {
	l := strings.ToLower(text)
	if preferZH {
		if containsAny(l, "specialist", "emergency consultation", "專科", "专科", "緊急診症", "紧急诊症") {
			return "專科或緊急獸醫診症"
		}
		if containsAny(l, "general consultation", "普通診症") {
			return "普通獸醫診症"
		}
		return "獸醫診症"
	}
	if containsAny(l, "specialist", "emergency consultation") {
		return "specialist or emergency veterinary consultation"
	}
	if containsAny(l, "general consultation") {
		return "general veterinary consultation"
	}
	return "veterinary consultation"
}

func inferBenefitLabel(text string, preferZH bool) string {
	l := strings.ToLower(text)
	if containsAny(l, "room and board", "confinement") {
		if preferZH {
			return "住院及病房"
		}
		return "room and board"
	}
	if containsAny(l, "consultation", "vet expenses") {
		if preferZH {
			return "獸醫診症"
		}
		return "veterinary consultation"
	}
	if containsAny(l, "chemotherapy", "heart diseases") {
		if preferZH {
			return "化療及心臟病治療"
		}
		return "chemotherapy and heart diseases treatment"
	}
	if preferZH {
		return "相關保障項目"
	}
	return "related benefit"
}

func formatPlanLimitsZh(limits planLimits) string {
	if len(limits) == 0 {
		return "目前證據內沒有明確限額"
	}
	plans := sortedPlans(limits)
	parts := make([]string, 0, len(plans))
	for _, plan := range plans {
		values := limits[plan]
		piece := "計劃" + plan + "：" + values["annual_limit"]
		if v := values["per_visit_limit"]; v != "" {
			piece += "，每次最多" + v
		}
		if v := values["per_day_limit"]; v != "" {
			piece += "，每天最多" + v
		}
		parts = append(parts, piece)
	}
	return strings.Join(parts, "；")
}

func formatPlanLimitsEn(limits planLimits) string {
	if len(limits) == 0 {
		return "no explicit limit in current evidence"
	}
	plans := sortedPlans(limits)
	parts := make([]string, 0, len(plans))
	for _, plan := range plans {
		values := limits[plan]
		piece := "Plan " + plan + ": " + values["annual_limit"]
		if v := values["per_visit_limit"]; v != "" {
			piece += ", " + v + " per visit"
		}
		if v := values["per_day_limit"]; v != "" {
			piece += ", " + v + " per day"
		}
		parts = append(parts, piece)
	}
	return strings.Join(parts, "; ")
}

func sortedPlans(limits planLimits) []string {
	plans := make([]string, 0, len(limits))
	for p := range limits {
		plans = append(plans, p)
	}
	sort.Strings(plans)
	return plans
}

func containsDirectConsultReference(text string) bool {
	return containsAny(text,
		"consultation",
		"consult carried out by a vet",
		"vet expenses made for the consultation",
		"veterinary consultation",
		"診症",
		"诊症",
		"診金",
		"獸醫",
		"兽医",
	)
}

func questionPrefersZH(question string) bool {
	for _, r := range question {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func detectQueryIntent(question, provider string) QueryIntent {
	normalized := strings.ToLower(strings.TrimSpace(question))
	targetProviders := inferTargetProviders(normalized)
	if provider != "" {
		targetProviders = []string{provider}
	}
	return QueryIntent{
		RawQuestion:      question,
		Normalized:       normalized,
		WantsComparison:  containsAny(normalized, "compare", "comparison", "比較", "对比"),
		WantsWaiting:     containsAny(normalized, "waiting period", "等待期", "等候期", "几多日", "幾多日", "多久", "多少日"),
		AsksDefinition:   containsAny(normalized, "what is", "meaning", "mean", "define", "解釋", "解释", "是什麼", "是什么", "意思"),
		WantsConsult:     containsAny(normalized, "consult", "consultation", "vet fee", "診症", "诊症", "獸醫", "兽医", "診金"),
		WantsCoverage:    containsAny(normalized, "cover", "coverage", "包唔包", "是否涵蓋", "是否覆盖", "賠唔賠", "赔不赔"),
		AsksLimit:        containsAny(normalized, "annual limit", "limit", "maximum", "max", "最高賠償額", "最高赔偿额", "上限", "每次", "每年"),
		WantsCancer:      containsAny(normalized, "cancer", "癌症"),
		WantsInjury:      containsAny(normalized, "injury", "bodily injury", "受傷", "受伤"),
		TargetProviders:  targetProviders,
		ProviderOverride: provider,
	}
}

func inferTargetProviders(lowerQuestion string) []string {
	providers := make([]string, 0, 2)
	for provider, aliases := range providerAliases {
		for _, alias := range aliases {
			if strings.Contains(lowerQuestion, strings.ToLower(alias)) {
				providers = append(providers, provider)
				break
			}
		}
	}
	sort.Strings(providers)
	return providers
}

func formatIntent(intent QueryIntent) string {
	parts := make([]string, 0, 8)
	if intent.WantsComparison {
		parts = append(parts, "comparison")
	}
	if intent.WantsWaiting {
		parts = append(parts, "waiting_period")
	}
	if intent.WantsConsult {
		parts = append(parts, "consult")
	}
	if intent.AsksLimit {
		parts = append(parts, "limit")
	}
	if intent.WantsCoverage {
		parts = append(parts, "coverage")
	}
	if len(intent.TargetProviders) > 0 {
		parts = append(parts, "providers="+strings.Join(intent.TargetProviders, "+"))
	}
	if len(parts) == 0 {
		return "general"
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func providerDisplay(provider string) string {
	if v, ok := providerDisplayNames[provider]; ok {
		return v
	}
	return provider
}

func formatClauseSuffix(clause string, preferZH bool) string {
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return ""
	}
	if preferZH {
		return "（條款：" + clause + "）"
	}
	return " (clause: " + clause + ")"
}

func formatWaitingPeriodLine(providerName, label string, days *int, fact waitingPeriodFact, preferZH bool) string {
	if preferZH {
		value := "不設等候期"
		if days != nil {
			value = strconv.Itoa(*days) + " 日"
		}
		return "- " + providerName + "：" + waitingLabelZH(label) + " = " + value + formatClauseSuffix(fact.Clauses, true)
	}
	value := "no waiting period"
	if days != nil {
		value = strconv.Itoa(*days) + " days"
	}
	return "- " + providerName + ": " + label + " = " + value + formatClauseSuffix(fact.Clauses, false)
}

func formatSingleWaitingPeriodLine(providerName, label string, days *int, fact waitingPeriodFact, preferZH bool) string {
	if preferZH {
		value := "不設等候期"
		if days != nil {
			value = strconv.Itoa(*days) + " 日"
		}
		return providerName + " 的" + waitingLabelZH(label) + "為 " + value + formatClauseSuffix(fact.Clauses, true)
	}
	value := "no waiting period"
	if days != nil {
		value = strconv.Itoa(*days) + " days"
	}
	return providerName + " " + label + " is " + value + formatClauseSuffix(fact.Clauses, false)
}

func waitingPeriodEvidenceSummary(fact waitingPeriodFact, preferZH bool) string {
	parts := make([]string, 0, 4)
	if preferZH {
		if fact.IllnessDays != nil {
			parts = append(parts, "疾病需等待 "+strconv.Itoa(*fact.IllnessDays)+" 日")
		}
		if fact.CancerDays != nil {
			parts = append(parts, "癌症需等待 "+strconv.Itoa(*fact.CancerDays)+" 日")
		}
		if fact.InjuryDays != nil {
			parts = append(parts, "受傷需等待 "+strconv.Itoa(*fact.InjuryDays)+" 日")
		}
		if fact.GeneralDays != nil {
			parts = append(parts, "一般條款列明需等待 "+strconv.Itoa(*fact.GeneralDays)+" 日")
		}
		if len(parts) == 0 && fact.NoWaitingPeriod {
			return "條款列明不設等候期"
		}
		return strings.Join(parts, "；")
	}

	if fact.IllnessDays != nil {
		parts = append(parts, "illness claims start after "+strconv.Itoa(*fact.IllnessDays)+" days")
	}
	if fact.CancerDays != nil {
		parts = append(parts, "cancer claims start after "+strconv.Itoa(*fact.CancerDays)+" days")
	}
	if fact.InjuryDays != nil {
		parts = append(parts, "injury claims start after "+strconv.Itoa(*fact.InjuryDays)+" days")
	}
	if fact.GeneralDays != nil {
		parts = append(parts, "the general clause starts after "+strconv.Itoa(*fact.GeneralDays)+" days")
	}
	if len(parts) == 0 && fact.NoWaitingPeriod {
		return "the clause says there is no waiting period"
	}
	return strings.Join(parts, ", ")
}

func waitingLabelZH(label string) string {
	switch label {
	case "cancer waiting period":
		return "癌症等候期"
	case "injury waiting period":
		return "受傷等候期"
	case "illness waiting period":
		return "疾病等候期"
	default:
		return "等候期"
	}
}

func reorderByScore(items []rankedChunk, fn func(Chunk) float64) []rankedChunk {
	withSignal := make([]rankedChunk, 0, len(items))
	withoutSignal := make([]rankedChunk, 0, len(items))
	for _, item := range items {
		if fn(item.chunk) > 0 {
			withSignal = append(withSignal, item)
		} else {
			withoutSignal = append(withoutSignal, item)
		}
	}
	sort.SliceStable(withSignal, func(i, j int) bool {
		si := fn(withSignal[i].chunk)
		sj := fn(withSignal[j].chunk)
		if math.Abs(si-sj) < 1e-6 {
			return withSignal[i].score > withSignal[j].score
		}
		return si > sj
	})
	out := make([]rankedChunk, 0, len(items))
	out = append(out, withSignal...)
	out = append(out, withoutSignal...)
	return out
}

func diversifyByProvider(items []rankedChunk) []rankedChunk {
	grouped := map[string][]rankedChunk{}
	order := make([]string, 0, 8)
	for _, item := range items {
		p := item.chunk.Metadata["provider"]
		if _, ok := grouped[p]; !ok {
			order = append(order, p)
		}
		grouped[p] = append(grouped[p], item)
	}
	sort.Strings(order)
	out := make([]rankedChunk, 0, len(items))
	for {
		progress := false
		for _, p := range order {
			queue := grouped[p]
			if len(queue) == 0 {
				continue
			}
			out = append(out, queue[0])
			grouped[p] = queue[1:]
			progress = true
		}
		if !progress {
			break
		}
	}
	return out
}

func waitingPeriodAnswerScore(intent QueryIntent, ch Chunk) float64 {
	src := buildSourcePayload(ch, 0)
	fact, ok := extractWaitingPeriodFact(src)
	if !ok {
		return 0
	}
	score := 1.0
	if intent.WantsCancer && fact.CancerDays != nil {
		score += 2
	}
	if intent.WantsInjury && fact.InjuryDays != nil {
		score += 2
	}
	if !intent.WantsCancer && !intent.WantsInjury {
		if fact.IllnessDays != nil {
			score += 1.6
		} else if fact.GeneralDays != nil {
			score += 1.2
		}
	}
	if fact.NoWaitingPeriod {
		score += 0.6
	}
	return score
}

func consultAnswerScore(intent QueryIntent, ch Chunk) float64 {
	src := buildSourcePayload(ch, 0)
	lower := strings.ToLower(src.Snippet)
	section := strings.ToLower(src.SectionPath)
	tags := strings.ToLower(src.TopicTags)
	score := 0.0
	if containsAny(tags, "consult") {
		score += 3
	}
	if containsAny(section, "consultation", "診症", "诊症", "診金") {
		score += 2
	}
	if containsDirectConsultReference(lower) {
		score += 2.5
	}
	if intent.AsksLimit && (hasExplicitLimitPattern(src.Snippet) || len(extractPlanLimits(src.Snippet)) > 0) {
		score += 1.8
	}
	return score
}

func genericLimitAnswerScore(intent QueryIntent, ch Chunk) float64 {
	src := buildSourcePayload(ch, 0)
	lower := strings.ToLower(src.Snippet)
	score := 0.0
	if hasExplicitLimitPattern(src.Snippet) {
		score += 2
	}
	if len(extractPlanLimits(src.Snippet)) > 0 {
		score += 2.2
	}
	if containsAny(strings.ToLower(src.TopicTags), "limit", "benefit") {
		score += 1.2
	}
	if containsAny(strings.ToLower(src.SectionPath), "benefits", "maximum", "limits", "最高賠償額", "最高赔偿额") {
		score += 1.2
	}
	if intent.WantsConsult && containsDirectConsultReference(lower) {
		score -= 1.8
	}
	return score
}

func chunkFromSource(src Source) Chunk {
	return Chunk{
		Text: src.Snippet,
		Metadata: map[string]string{
			"provider":     src.Provider,
			"source_name":  src.SourceName,
			"section_path": src.SectionPath,
			"clauses":      src.Clauses,
			"unit_types":   src.UnitTypes,
			"topic_tags":   src.TopicTags,
			"language":     src.Language,
		},
	}
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
