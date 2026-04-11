package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fakeGateway struct {
	send func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error)
}

func (f fakeGateway) Send(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
	return f.send(ctx, method, path, query, body, headers)
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", url.QueryEscape(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.AppBookingMirror{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestCreateAppBookingPersistsMirror(t *testing.T) {
	db := newTestDB(t)
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		if method != http.MethodPost {
			t.Fatalf("unexpected method: %s", method)
		}
		if path != "/app/v1/vaccinations/bookings" {
			t.Fatalf("unexpected path: %s", path)
		}
		if headers["Idempotency-Key"] == "" {
			t.Fatal("expected idempotency key")
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-1","clinic_integration_id":"clinic-1","status":"requested","scheduled_at":"2026-04-12T10:00:00Z","created_at":"2026-04-11T12:00:00Z","updated_at":"2026-04-11T12:00:00Z","merchant_status":{"appointment_status":"pending"}}}`), nil
	}}

	payload := map[string]any{
		"clinic_id":      "clinic-1",
		"pet_id":         "pet-123",
		"service_type":   "vaccine",
		"scheduled_date": "2026-04-12T10:00:00Z",
		"notes":          "OwnerName: Ada | OwnerEmail: ada@example.com | OwnerPhone: +85212345678 | Pet: Mochi",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var mirrors []models.AppBookingMirror
	if err := db.Find(&mirrors).Error; err != nil {
		t.Fatalf("query mirrors: %v", err)
	}
	if len(mirrors) != 1 {
		t.Fatalf("expected 1 mirror, got %d", len(mirrors))
	}
	if mirrors[0].PetName != "Mochi" {
		t.Fatalf("expected pet name Mochi, got %q", mirrors[0].PetName)
	}
	if mirrors[0].Status != "pending" {
		t.Fatalf("expected pending status, got %q", mirrors[0].Status)
	}
}

func TestListAppBookingsRefreshesMirrorStatus(t *testing.T) {
	db := newTestDB(t)
	mirror := models.AppBookingMirror{
		ID:                "mirror-1",
		ExternalBookingID: "ext-1",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-1","clinic_integration_id":"clinic-1","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-1").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %q", refreshed.Status)
	}
}
