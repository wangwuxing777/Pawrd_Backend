package handlers

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
)

func TestShippingAddressesPutThenGet(t *testing.T) {
	db := setupUnifiedProfileTestDB(t)
	_, token := seedUnifiedUser(t, db, "address@example.com", "Address Owner")

	rec, req := authedRequest(t, http.MethodPut, "/api/profile/shipping-addresses", token, map[string]any{
		"addresses": []map[string]any{
			{
				"id":             "addr-1",
				"label":          "Home",
				"recipient_name": "Alice",
				"phone":          "61234567",
				"address":        "1 Home Road",
				"region_id":      "hongKongIsland",
				"district_id":    "centralAndWestern",
			},
			{
				"id":             "addr-2",
				"label":          "Office",
				"recipient_name": "Alice",
				"phone":          "+852 9123 4567",
				"address":        "9 Office Road",
				"region_id":      "kowloon",
				"district_id":    "yauTsimMong",
			},
		},
	})
	NewShippingAddressesHandler(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	rec, req = authedRequest(t, http.MethodGet, "/api/profile/shipping-addresses", token, nil)
	NewShippingAddressesHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Addresses []models.ShippingAddress `json:"addresses"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Addresses) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(payload.Addresses))
	}
	if payload.Addresses[0].ID != "addr-1" ||
		payload.Addresses[0].SortOrder != 0 ||
		payload.Addresses[0].Phone != "+85261234567" {
		t.Fatalf("unexpected first address: %+v", payload.Addresses[0])
	}
	if payload.Addresses[1].ID != "addr-2" ||
		payload.Addresses[1].SortOrder != 1 ||
		payload.Addresses[1].Phone != "+85291234567" {
		t.Fatalf("unexpected second address: %+v", payload.Addresses[1])
	}
}

func TestShippingAddressesRequiresAuth(t *testing.T) {
	db := setupUnifiedProfileTestDB(t)
	rec, req := authedRequest(t, http.MethodGet, "/api/profile/shipping-addresses", "", nil)
	NewShippingAddressesHandler(db).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
