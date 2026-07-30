package handlers

import (
	"strings"
	"testing"
)

func TestRankInsuranceCandidatesFiltersAndDiversifies(t *testing.T) {
	candidates := []insuranceCandidate{
		{
			id: 1, provider: "One Degree", plan: "Prestige Plan",
			minAge: "13 weeks", maxAge: "11 years", suitablePet: "cat, dog",
			coinsurance: "Network Clinic 90%", medicalLimit: 100000,
			subcoverages: []insuranceSubcoverage{
				{name: "Clinical and Surgical", remark: "Surgery, X-Ray, MRI & CT Coverage"},
			},
		},
		{
			id: 2, provider: "One Degree", plan: "Ultra Plan",
			minAge: "13 weeks", maxAge: "11 years", suitablePet: "cat, dog",
			coinsurance: "Network Clinic 90%", medicalLimit: 100000,
		},
		{
			id: 3, provider: "Prudential", plan: "PRUChoice Furkid Care - B",
			minAge: "13 weeks", maxAge: "8 years", suitablePet: "cat, dog",
			coinsurance: "30%", medicalLimit: 90000,
			subcoverages: []insuranceSubcoverage{
				{name: "Veterinary Consultation", limit: "16000"},
				{name: "Clinical and Surgical", remark: "Surgery including CT and MRI scans"},
			},
		},
		{
			id: 4, provider: "MSIG", plan: "Dog Ultimate",
			minAge: "16 weeks", maxAge: "9 years", suitablePet: "dog",
			medicalLimit: 200000,
		},
		{
			id: 5, provider: "Blue Cross", plan: "Sharing Plan",
			minAge: "13 weeks", maxAge: "12 years", suitablePet: "cat, dog",
			medicalLimit: 4500,
		},
	}

	result := rankInsuranceCandidates(insuranceRecommendationRequest{
		PetName: "Buddy", PetSpecies: "cat", PetAgeYears: 4,
		MedicalSummary: "Confirmed patellar luxation.",
	}, candidates, 3)

	if len(result) != 3 {
		t.Fatalf("expected 3 recommendations, got %#v", result)
	}
	if result[0].Plan != "PRUChoice Furkid Care - B" {
		t.Fatalf("expected strongest eligible plan first, got %#v", result)
	}
	if result[0].Provider == result[1].Provider {
		t.Fatalf("expected provider diversity, got %#v", result)
	}
	for _, recommendation := range result {
		if strings.Contains(recommendation.Plan, "Dog") || strings.Contains(recommendation.Plan, "Sharing") {
			t.Fatalf("ineligible plan returned: %#v", recommendation)
		}
		if len(recommendation.Caveats) == 0 ||
			!strings.Contains(strings.ToLower(recommendation.Caveats[0]), "pre-existing") {
			t.Fatalf("expected pre-existing-condition caveat: %#v", recommendation)
		}
	}
}

func TestAgeEligibleUnderstandsWeeksMonthsAndYears(t *testing.T) {
	if !ageEligible(4, "13 weeks", "8 years") {
		t.Fatal("expected a 4-year-old pet to be eligible")
	}
	if ageEligible(9, "6 months", "8 years") {
		t.Fatal("expected a 9-year-old pet to be ineligible")
	}
}
