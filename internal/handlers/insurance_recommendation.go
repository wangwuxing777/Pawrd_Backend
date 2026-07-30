package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type insuranceRecommendationRequest struct {
	PetName        string `json:"pet_name"`
	PetSpecies     string `json:"pet_species"`
	PetBreed       string `json:"pet_breed"`
	PetAgeYears    int    `json:"pet_age_years"`
	MedicalSummary string `json:"medical_summary"`
}

type insuranceRecommendation struct {
	InsuranceID    int      `json:"insurance_id"`
	Provider       string   `json:"provider"`
	Plan           string   `json:"plan"`
	Score          int      `json:"score"`
	MedicalLimit   int      `json:"medical_limit"`
	Highlights     []string `json:"highlights"`
	Caveats        []string `json:"caveats"`
	InformationURL string   `json:"information_url,omitempty"`
}

type insuranceRecommendationResponse struct {
	Answer          string                    `json:"answer"`
	Recommendations []insuranceRecommendation `json:"recommendations"`
	Method          string                    `json:"method"`
}

type insuranceCandidate struct {
	id             int
	provider       string
	plan           string
	minAge         string
	maxAge         string
	coinsurance    string
	suitablePet    string
	waitingPeriod  string
	informationURL string
	medicalLimit   int
	subcoverages   []insuranceSubcoverage
}

type insuranceSubcoverage struct {
	name   string
	limit  string
	remark string
}

func NewInsuranceRecommendationHandler() http.HandlerFunc {
	return newInsuranceRecommendationHandler(OpenInsuranceDB)
}

func newInsuranceRecommendationHandler(openDB func() (*sql.DB, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req insuranceRecommendationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		req.PetSpecies = strings.ToLower(strings.TrimSpace(req.PetSpecies))
		if req.PetSpecies != "cat" && req.PetSpecies != "dog" {
			http.Error(w, "pet_species must be cat or dog", http.StatusBadRequest)
			return
		}
		if req.PetAgeYears < 0 {
			http.Error(w, "pet_age_years must not be negative", http.StatusBadRequest)
			return
		}

		db, err := openDB()
		if err != nil {
			http.Error(w, "insurance database unavailable", http.StatusInternalServerError)
			return
		}
		defer db.Close()

		candidates, err := loadInsuranceCandidates(db)
		if err != nil {
			http.Error(w, "insurance products unavailable", http.StatusInternalServerError)
			return
		}
		recommendations := rankInsuranceCandidates(req, candidates, 3)
		if len(recommendations) == 0 {
			http.Error(w, "no eligible insurance products found", http.StatusUnprocessableEntity)
			return
		}

		response := insuranceRecommendationResponse{
			Answer:          formatInsuranceRecommendations(req, recommendations),
			Recommendations: recommendations,
			Method:          "deterministic_db_v1",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}
}

func loadInsuranceCandidates(db *sql.DB) ([]insuranceCandidate, error) {
	rows, err := db.Query(`
		SELECT p.insurance_id, ip.company_name, p.insurance_name,
		       COALESCE(p.min_age, ''), COALESCE(p.max_age, ''),
		       COALESCE(p.coinsurance, ''), COALESCE(p.suitable_pet_type, ''),
		       COALESCE(p.waiting_period, ''), COALESCE(p.information_link, ''),
		       COALESCE(cl.coverage_limit, 0)
		FROM product p
		JOIN insurance_provider ip ON ip.company_id = p.provider_id
		LEFT JOIN coverage_limit cl
		       ON cl.product_id = p.insurance_id AND cl.coverage_id = 1
		ORDER BY p.insurance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []insuranceCandidate
	for rows.Next() {
		var c insuranceCandidate
		if err := rows.Scan(
			&c.id, &c.provider, &c.plan, &c.minAge, &c.maxAge,
			&c.coinsurance, &c.suitablePet, &c.waitingPeriod,
			&c.informationURL, &c.medicalLimit,
		); err != nil {
			return nil, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range candidates {
		subRows, err := db.Query(`
			SELECT COALESCE(sub_coverage_name, ''), COALESCE(sub_limit, ''),
			       COALESCE(sub_coverage_remark, '')
			FROM sub_coverage_limit
			WHERE product_id = ?`, candidates[i].id)
		if err != nil {
			return nil, err
		}
		for subRows.Next() {
			var sub insuranceSubcoverage
			if err := subRows.Scan(&sub.name, &sub.limit, &sub.remark); err != nil {
				subRows.Close()
				return nil, err
			}
			candidates[i].subcoverages = append(candidates[i].subcoverages, sub)
		}
		subRows.Close()
	}
	return candidates, nil
}

func rankInsuranceCandidates(req insuranceRecommendationRequest, candidates []insuranceCandidate, limit int) []insuranceRecommendation {
	var ranked []insuranceRecommendation
	for _, candidate := range candidates {
		if !strings.Contains(strings.ToLower(candidate.suitablePet), req.PetSpecies) {
			continue
		}
		if !ageEligible(req.PetAgeYears, candidate.minAge, candidate.maxAge) {
			continue
		}
		if strings.Contains(strings.ToLower(candidate.plan), "sharing") {
			continue
		}

		score := 40
		if candidate.medicalLimit > 0 {
			score += minInt(25, candidate.medicalLimit/4000)
		}
		highlights := []string{}
		if candidate.medicalLimit > 0 {
			highlights = append(highlights, fmt.Sprintf("Annual medical limit HK$%s", commaInt(candidate.medicalLimit)))
		}

		allDetails := strings.ToLower(candidate.plan + " " + candidate.coinsurance)
		for _, sub := range candidate.subcoverages {
			allDetails += " " + strings.ToLower(sub.name+" "+sub.remark)
			if strings.Contains(strings.ToLower(sub.name), "veterinary consultation") {
				if amount := firstInteger(sub.limit); amount > 0 {
					score += minInt(10, amount/1000)
					highlights = append(highlights, fmt.Sprintf("Veterinary consultation: %s", formatLimit(sub.limit)))
				}
			}
		}
		if strings.Contains(allDetails, "mri") || strings.Contains(allDetails, "ct ") || strings.Contains(allDetails, "ct coverage") {
			score += 8
			highlights = append(highlights, "Advanced imaging (CT/MRI) listed")
		} else if strings.Contains(allDetails, "x-ray") || strings.Contains(allDetails, "ultrasound") {
			score += 5
			highlights = append(highlights, "X-ray/ultrasound listed")
		}
		if strings.Contains(allDetails, "surg") {
			score += 7
			highlights = append(highlights, "Surgical cover listed")
		}
		score += reimbursementScore(candidate.coinsurance)
		if len(highlights) == 0 {
			highlights = append(highlights, "Eligible by the current age and species data")
		}

		caveats := []string{}
		if strings.TrimSpace(req.MedicalSummary) != "" {
			caveats = append(caveats, "The recorded medical condition may be treated as pre-existing; related claims are not assumed covered.")
		}
		if strings.TrimSpace(candidate.waitingPeriod) != "" {
			caveats = append(caveats, compactText(candidate.waitingPeriod, 180))
		}
		if strings.TrimSpace(candidate.coinsurance) != "" {
			caveats = append(caveats, "Cost sharing: "+compactText(candidate.coinsurance, 140))
		}
		ranked = append(ranked, insuranceRecommendation{
			InsuranceID:    candidate.id,
			Provider:       strings.TrimSpace(candidate.provider),
			Plan:           strings.TrimSpace(candidate.plan),
			Score:          minInt(99, score),
			MedicalLimit:   candidate.medicalLimit,
			Highlights:     uniqueStrings(highlights),
			Caveats:        caveats,
			InformationURL: strings.TrimSpace(candidate.informationURL),
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].MedicalLimit > ranked[j].MedicalLimit
		}
		return ranked[i].Score > ranked[j].Score
	})

	// A first-version recommendation is more useful when it compares insurers
	// instead of returning several tiers from the same company.
	selected := make([]insuranceRecommendation, 0, limit)
	seenProviders := map[string]bool{}
	for _, recommendation := range ranked {
		key := strings.ToLower(recommendation.Provider)
		if seenProviders[key] {
			continue
		}
		seenProviders[key] = true
		selected = append(selected, recommendation)
		if len(selected) == limit {
			return selected
		}
	}
	for _, recommendation := range ranked {
		alreadySelected := false
		for _, selectedRecommendation := range selected {
			if selectedRecommendation.InsuranceID == recommendation.InsuranceID {
				alreadySelected = true
				break
			}
		}
		if !alreadySelected {
			selected = append(selected, recommendation)
		}
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func formatInsuranceRecommendations(req insuranceRecommendationRequest, recommendations []insuranceRecommendation) string {
	name := strings.TrimSpace(req.PetName)
	if name == "" {
		name = "your pet"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "First-version recommendations for %s, based on the current insurance comparison database:\n\n", name)
	for i, recommendation := range recommendations {
		fmt.Fprintf(&b, "%d. %s — %s (match %d/100)\n", i+1, recommendation.Provider, recommendation.Plan, recommendation.Score)
		for _, highlight := range recommendation.Highlights {
			fmt.Fprintf(&b, "• %s\n", highlight)
		}
		for _, caveat := range recommendation.Caveats {
			fmt.Fprintf(&b, "• Important: %s\n", caveat)
		}
		b.WriteString("\n")
	}
	b.WriteString("This is a preliminary comparison, not confirmation of coverage. Ask each insurer for written confirmation of exclusions, especially for any condition already recorded before enrolment.")
	return strings.TrimSpace(b.String())
}

func ageEligible(ageYears int, minAge, maxAge string) bool {
	minimum := ageInYears(minAge)
	maximum := ageInYears(maxAge)
	age := float64(ageYears)
	return (minimum < 0 || age >= minimum) && (maximum < 0 || age <= maximum)
}

func ageInYears(value string) float64 {
	lower := strings.ToLower(strings.TrimSpace(value))
	number := firstNumber(lower)
	if number < 0 {
		return -1
	}
	switch {
	case strings.Contains(lower, "week"):
		return number / 52
	case strings.Contains(lower, "month"):
		return number / 12
	default:
		return number
	}
}

var numberPattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

func firstNumber(value string) float64 {
	match := numberPattern.FindString(value)
	if match == "" {
		return -1
	}
	number, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return -1
	}
	return number
}

func firstInteger(value string) int {
	number := firstNumber(strings.ReplaceAll(value, ",", ""))
	if number < 0 {
		return 0
	}
	return int(number)
}

func reimbursementScore(value string) int {
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "90%"):
		return 10
	case strings.Contains(lower, "20%"):
		return 9
	case strings.Contains(lower, "30%"):
		return 7
	case strings.Contains(lower, "40%"):
		return 4
	default:
		return 0
	}
}

func formatLimit(value string) string {
	if amount := firstInteger(value); amount > 0 {
		return "HK$" + commaInt(amount)
	}
	return strings.TrimSpace(value)
}

func commaInt(value int) string {
	raw := strconv.Itoa(value)
	for i := len(raw) - 3; i > 0; i -= 3 {
		raw = raw[:i] + "," + raw[i:]
	}
	return raw
}

func compactText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "…"
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
