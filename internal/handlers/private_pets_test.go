package handlers

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestPrivatePetsPutThenGet(t *testing.T) {
	db := setupUnifiedProfileTestDB(t)
	_, token := seedUnifiedUser(t, db, "private-pets@example.com", "Private Pets")

	rec, req := authedRequest(t, http.MethodPut, "/api/profile/pets", token, map[string]any{
		"pets": []map[string]any{
			{
				"id":           "pet-1",
				"name":         "Mochi",
				"species":      "Cat",
				"breed":        "British Shorthair",
				"sex":          "Female",
				"birth_year":   2022,
				"weight_kg":    4.2,
				"is_neutered":  true,
				"microchip_id": "CHIP-1",
				"allergies":    "chicken",
				"notes":        "quiet",
				"vaccinations": []map[string]any{
					{"name": "Rabies", "date": "2023-01-02T00:00:00Z"},
				},
				"medical_visits": []map[string]any{
					{
						"date": "2024-01-02T00:00:00Z", "clinic": "Pet Clinic",
						"vet": "Dr. A", "reason": "checkup", "diagnosis": "healthy",
						"treatment": "none", "cost": 100.5, "notes": "ok",
					},
				},
				"medications": []map[string]any{
					{
						"name": "Vitamin", "dosage": "1 pill", "frequency": "daily",
						"start_date": "2024-02-01T00:00:00Z",
						"end_date":   "2024-03-01T00:00:00Z", "notes": "morning",
					},
				},
				"weight_entries": []map[string]any{
					{"date": "2024-02-01T00:00:00Z", "weight_kg": 4.1, "notes": ""},
				},
			},
		},
	})
	NewPrivatePetsHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec, req = authedRequest(t, http.MethodGet, "/api/profile/pets", token, nil)
	NewPrivatePetsHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Pets []privatePetDTO `json:"pets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Pets) != 1 {
		t.Fatalf("expected 1 pet, got %d", len(payload.Pets))
	}
	pet := payload.Pets[0]
	if pet.ID != "pet-1" || pet.Name != "Mochi" || pet.MicrochipID != "CHIP-1" ||
		pet.Allergies != "chicken" || !pet.IsNeutered || pet.WeightKg == nil ||
		*pet.WeightKg != 4.2 {
		t.Fatalf("unexpected private pet: %+v", pet)
	}
	if len(pet.Vaccinations) != 1 || len(pet.MedicalVisits) != 1 ||
		len(pet.Medications) != 1 || len(pet.WeightEntries) != 1 {
		t.Fatalf("nested private records missing: %+v", pet)
	}
}

func TestPrivatePetsRequiresAuth(t *testing.T) {
	db := setupUnifiedProfileTestDB(t)
	rec, req := authedRequest(t, http.MethodGet, "/api/profile/pets", "", nil)
	NewPrivatePetsHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
