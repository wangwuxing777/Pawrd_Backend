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
		if headers["X-Request-ID"] == "" {
			t.Fatal("expected request id header")
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-1","clinic_integration_id":"clinic-1","status":"requested","scheduled_at":"2026-04-12T10:00:00Z","created_at":"2026-04-11T12:00:00Z","updated_at":"2026-04-11T12:00:00Z","merchant_status":{"appointment_status":"pending","internal_appointment_id":101}}}`), nil
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
	if mirrors[0].IdempotencyKey == "" {
		t.Fatal("expected persisted idempotency key")
	}
	if mirrors[0].BookingClinicID != "clinic-1" {
		t.Fatalf("expected booking clinic id clinic-1, got %q", mirrors[0].BookingClinicID)
	}
	if mirrors[0].MerchantInternalAppointmentID == nil || *mirrors[0].MerchantInternalAppointmentID != 101 {
		t.Fatalf("expected internal appointment id 101, got %v", mirrors[0].MerchantInternalAppointmentID)
	}
	if mirrors[0].MerchantUpdatedAt == nil {
		t.Fatal("expected merchant updated timestamp")
	}
	if mirrors[0].LastSyncAttemptAt == nil {
		t.Fatal("expected last sync attempt timestamp")
	}
	if mirrors[0].LastSyncedAt == nil {
		t.Fatal("expected last synced timestamp")
	}
	if mirrors[0].LastSyncError != "" {
		t.Fatalf("expected empty last sync error, got %q", mirrors[0].LastSyncError)
	}
	if mirrors[0].RequestID == "" {
		t.Fatal("expected persisted request id")
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("expected response request id header")
	}
}

func TestCreateAppBookingUsesIncomingRequestID(t *testing.T) {
	db := newTestDB(t)
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		if headers["X-Request-ID"] != "req-123" {
			t.Fatalf("expected request id req-123, got %q", headers["X-Request-ID"])
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-req","clinic_integration_id":"clinic-1","status":"requested","scheduled_at":"2026-04-12T10:00:00Z","created_at":"2026-04-11T12:00:00Z","updated_at":"2026-04-11T12:00:00Z","merchant_status":{"appointment_status":"pending"}}}`), nil
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
	req.Header.Set("X-Request-ID", "req-123")
	rec := httptest.NewRecorder()

	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var mirror models.AppBookingMirror
	if err := db.First(&mirror).Error; err != nil {
		t.Fatalf("query mirror: %v", err)
	}
	if mirror.RequestID != "req-123" {
		t.Fatalf("expected persisted request id req-123, got %q", mirror.RequestID)
	}
	if rec.Header().Get("X-Request-ID") != "req-123" {
		t.Fatalf("expected response request id req-123, got %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestCreateAppBookingUsesBookingClinicIDForMerchantRequest(t *testing.T) {
	db := newTestDB(t)
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		var merchantReq struct {
			ClinicIntegrationID string `json:"clinic_integration_id"`
		}
		if err := json.Unmarshal(body, &merchantReq); err != nil {
			t.Fatalf("decode merchant request: %v", err)
		}
		if merchantReq.ClinicIntegrationID != "clinic_happypaws_hk" {
			t.Fatalf("expected booking clinic id clinic_happypaws_hk, got %q", merchantReq.ClinicIntegrationID)
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-2","clinic_integration_id":"clinic_happypaws_hk","status":"requested","scheduled_at":"2026-04-12T10:00:00Z","created_at":"2026-04-11T12:00:00Z","updated_at":"2026-04-11T12:00:00Z","merchant_status":{"appointment_status":"pending"}}}`), nil
	}}

	payload := map[string]any{
		"clinic_id":         "74",
		"booking_clinic_id": "clinic_happypaws_hk",
		"pet_id":            "pet-123",
		"service_type":      "vaccine",
		"scheduled_date":    "2026-04-12T10:00:00Z",
		"notes":             "OwnerName: Ada | OwnerEmail: ada@example.com | OwnerPhone: +85212345678 | Pet: Mochi",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var mirror models.AppBookingMirror
	if err := db.First(&mirror).Error; err != nil {
		t.Fatalf("query mirror: %v", err)
	}
	if mirror.ClinicID != "74" {
		t.Fatalf("expected mirror to preserve public clinic_id 74, got %q", mirror.ClinicID)
	}
	if mirror.BookingClinicID != "clinic_happypaws_hk" {
		t.Fatalf("expected persisted booking clinic id clinic_happypaws_hk, got %q", mirror.BookingClinicID)
	}
	if mirror.IdempotencyKey == "" {
		t.Fatal("expected persisted idempotency key")
	}
}

func TestCreateAppBookingFallsBackToMappedBookingClinicID(t *testing.T) {
	db := newTestDB(t)
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		var merchantReq struct {
			ClinicIntegrationID string `json:"clinic_integration_id"`
		}
		if err := json.Unmarshal(body, &merchantReq); err != nil {
			t.Fatalf("decode merchant request: %v", err)
		}
		if merchantReq.ClinicIntegrationID != "clinic_happypaws_hk" {
			t.Fatalf("expected mapped clinic_happypaws_hk, got %q", merchantReq.ClinicIntegrationID)
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-3","clinic_integration_id":"clinic_happypaws_hk","status":"requested","scheduled_at":"2026-04-12T10:00:00Z","created_at":"2026-04-11T12:00:00Z","updated_at":"2026-04-11T12:00:00Z","merchant_status":{"appointment_status":"pending"}}}`), nil
	}}

	payload := map[string]any{
		"clinic_id":      "74",
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
}

func TestCreateAppBookingPersistsProvidedClinicName(t *testing.T) {
	db := newTestDB(t)
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-4","clinic_integration_id":"clinic_happypaws_hk","status":"requested","scheduled_at":"2026-04-12T10:00:00Z","created_at":"2026-04-11T12:00:00Z","updated_at":"2026-04-11T12:00:00Z","merchant_status":{"appointment_status":"pending"}}}`), nil
	}}

	payload := map[string]any{
		"clinic_id":         "74",
		"booking_clinic_id": "clinic_happypaws_hk",
		"clinic_name":       "Pawrd Test Clinic",
		"pet_id":            "pet-123",
		"service_type":      "vaccine",
		"scheduled_date":    "2026-04-12T10:00:00Z",
		"notes":             "OwnerName: Ada | OwnerEmail: ada@example.com | OwnerPhone: +85212345678 | Pet: Mochi",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var mirror models.AppBookingMirror
	if err := db.First(&mirror).Error; err != nil {
		t.Fatalf("query mirror: %v", err)
	}
	if mirror.ClinicName != "Pawrd Test Clinic" {
		t.Fatalf("expected provided clinic name, got %q", mirror.ClinicName)
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
		LastSyncError:     "upstream_status_Bad Gateway",
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		if headers["X-Request-ID"] != "list-req-1" {
			t.Fatalf("expected request id list-req-1 in refresh call, got %q", headers["X-Request-ID"])
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-1","clinic_integration_id":"clinic-1","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked","internal_appointment_id":202}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings", nil)
	req.Header.Set("X-Request-ID", "list-req-1")
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "list-req-1" {
		t.Fatalf("expected response X-Request-ID list-req-1, got %q", rec.Header().Get("X-Request-ID"))
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-1").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %q", refreshed.Status)
	}
	if refreshed.MerchantUpdatedAt == nil {
		t.Fatal("expected merchant updated timestamp after refresh")
	}
	if refreshed.LastSyncedAt == nil {
		t.Fatal("expected last synced timestamp after refresh")
	}
	if refreshed.LastSyncAttemptAt == nil {
		t.Fatal("expected last sync attempt timestamp after refresh")
	}
	if refreshed.LastSyncError != "" {
		t.Fatalf("expected cleared last sync error after refresh, got %q", refreshed.LastSyncError)
	}
	if refreshed.RequestID != "list-req-1" {
		t.Fatalf("expected request id list-req-1, got %q", refreshed.RequestID)
	}
	if refreshed.LastSyncSource != "read_refresh" {
		t.Fatalf("expected read_refresh sync source, got %q", refreshed.LastSyncSource)
	}
	expectedUpdatedAt := time.Date(2026, 4, 11, 12, 30, 0, 0, time.UTC)
	if !refreshed.MerchantUpdatedAt.Equal(expectedUpdatedAt) {
		t.Fatalf("expected merchant updated at %s, got %v", expectedUpdatedAt, refreshed.MerchantUpdatedAt)
	}
	if refreshed.MerchantInternalAppointmentID == nil || *refreshed.MerchantInternalAppointmentID != 202 {
		t.Fatalf("expected internal appointment id 202, got %v", refreshed.MerchantInternalAppointmentID)
	}
}

func TestListAppBookingsSkipsRefreshWhenMirrorIsFresh(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	mirror := models.AppBookingMirror{
		ID:                "mirror-fresh-skip-1",
		ExternalBookingID: "ext-fresh-skip-1",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	called := false
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		called = true
		return http.StatusOK, "application/json", []byte(`{}`), nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings", nil)
	req.Header.Set("X-Request-ID", "list-req-fresh")
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "list-req-fresh" {
		t.Fatalf("expected response X-Request-ID list-req-fresh, got %q", rec.Header().Get("X-Request-ID"))
	}
	if called {
		t.Fatal("expected fresh mirror to skip refresh")
	}
}

func TestListAppBookingsRejectsInvalidSyncStateFilter(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/api/bookings?sync_state=bad", nil)
	rec := httptest.NewRecorder()

	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAppBookingsCanFilterBySyncState(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	freshSyncedAt := now
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	fresh := models.AppBookingMirror{
		ID:                "mirror-filter-fresh",
		ExternalBookingID: "ext-filter-fresh",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &freshSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	stale := models.AppBookingMirror{
		ID:                "mirror-filter-stale",
		ExternalBookingID: "ext-filter-stale",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-2",
		PetName:           "Latte",
	}
	if err := db.Create(&fresh).Error; err != nil {
		t.Fatalf("seed fresh mirror: %v", err)
	}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatalf("seed stale mirror: %v", err)
	}

	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-filter-stale","clinic_integration_id":"clinic-2","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?sync_state=stale", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 stale booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].ClinicID != "clinic-2" {
		t.Fatalf("expected clinic-2 stale booking, got %q", body.Bookings[0].ClinicID)
	}
	if body.Bookings[0].SyncState != "stale" && body.Bookings[0].SyncState != "fresh" {
		t.Fatalf("expected a valid sync state after filtering, got %q", body.Bookings[0].SyncState)
	}
}

func TestListAppBookingsCanFilterByExternalBookingIDAndRequestID(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	lastSyncedAt := now
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-filter-a",
		ExternalBookingID: "ext-filter-a",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		RequestID:         "req-a",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-filter-b",
		ExternalBookingID: "ext-filter-b",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		RequestID:         "req-b",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-2",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?external_booking_id=ext-filter-b&request_id=req-b", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].ClinicID != "clinic-2" {
		t.Fatalf("expected clinic-2 booking, got %q", body.Bookings[0].ClinicID)
	}
}

func TestListAppBookingsRejectsInvalidLastSyncSourceFilter(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/api/bookings?last_sync_source=bad", nil)
	rec := httptest.NewRecorder()

	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAppBookingsCanFilterByLastSyncSource(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	lastSyncedAt := now
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-source-a",
		ExternalBookingID: "ext-source-a",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncSource:    "merchant_sync",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-source-b",
		ExternalBookingID: "ext-source-b",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncSource:    "create_accept",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-2",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?last_sync_source=merchant_sync", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].ClinicID != "clinic-1" {
		t.Fatalf("expected clinic-1 booking, got %q", body.Bookings[0].ClinicID)
	}
}

func TestListAppBookingsIncludesDebugOnlyWhenRequested(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	lastSyncedAt := now
	internalID := uint(202)
	mirror := models.AppBookingMirror{
		ID:                            "mirror-debug-list",
		ExternalBookingID:             "ext-debug-list",
		ClinicID:                      "clinic-1",
		BookingClinicID:               "clinic_happypaws_hk",
		MerchantInternalAppointmentID: &internalID,
		ClinicName:                    "Clinic One",
		ServiceType:                   "vaccine",
		ScheduledDate:                 now,
		Status:                        "pending",
		RequestID:                     "req-debug-list",
		IdempotencyKey:                "idem-debug-list",
		LastSyncedAt:                  &lastSyncedAt,
		PetID:                         "pet-1",
		PetName:                       "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	bookings := body["bookings"].([]any)
	first := bookings[0].(map[string]any)
	if _, ok := first["debug"]; ok {
		t.Fatal("expected debug to be absent by default")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/bookings?include_debug=true", nil)
	rec = httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	bookings = body["bookings"].([]any)
	first = bookings[0].(map[string]any)
	debug := first["debug"].(map[string]any)
	if debug["booking_clinic_id"] != "clinic_happypaws_hk" {
		t.Fatalf("expected booking_clinic_id in debug, got %#v", debug["booking_clinic_id"])
	}
	if debug["request_id"] != "req-debug-list" {
		t.Fatalf("expected request_id in debug, got %#v", debug["request_id"])
	}
}

func TestListAppBookingsCanFilterByMerchantAnchors(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	lastSyncedAt := now
	internalID1 := uint(101)
	internalID2 := uint(202)
	mirrorA := models.AppBookingMirror{
		ID:                            "mirror-anchor-a",
		ExternalBookingID:             "ext-anchor-a",
		ClinicID:                      "clinic-1",
		BookingClinicID:               "clinic_happypaws_hk",
		MerchantInternalAppointmentID: &internalID1,
		ClinicName:                    "Clinic One",
		ServiceType:                   "vaccine",
		ScheduledDate:                 now,
		Status:                        "pending",
		LastSyncedAt:                  &lastSyncedAt,
		PetID:                         "pet-1",
		PetName:                       "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                            "mirror-anchor-b",
		ExternalBookingID:             "ext-anchor-b",
		ClinicID:                      "clinic-2",
		BookingClinicID:               "clinic_other_hk",
		MerchantInternalAppointmentID: &internalID2,
		ClinicName:                    "Clinic Two",
		ServiceType:                   "vaccine",
		ScheduledDate:                 now,
		Status:                        "pending",
		LastSyncedAt:                  &lastSyncedAt,
		PetID:                         "pet-2",
		PetName:                       "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?booking_clinic_id=clinic_other_hk&merchant_internal_appointment_id=202", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].ClinicID != "clinic-2" {
		t.Fatalf("expected clinic-2 booking, got %q", body.Bookings[0].ClinicID)
	}
}

func TestListAppBookingsCanFilterByPetID(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	lastSyncedAt := now
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-pet-a",
		ExternalBookingID: "ext-pet-a",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-a",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-pet-b",
		ExternalBookingID: "ext-pet-b",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-b",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?pet_id=pet-b", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].PetID != "pet-b" {
		t.Fatalf("expected pet-b booking, got %q", body.Bookings[0].PetID)
	}
}

func TestListAppBookingsCanFilterByClinicID(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	lastSyncedAt := now
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-clinic-a",
		ExternalBookingID: "ext-clinic-a",
		ClinicID:          "clinic-a",
		ClinicName:        "Clinic A",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-a",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-clinic-b",
		ExternalBookingID: "ext-clinic-b",
		ClinicID:          "clinic-b",
		ClinicName:        "Clinic B",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-b",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?clinic_id=clinic-b", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].ClinicID != "clinic-b" {
		t.Fatalf("expected clinic-b booking, got %q", body.Bookings[0].ClinicID)
	}
}

func TestListAppBookingsRejectsInvalidSince(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/api/bookings?since=bad", nil)
	rec := httptest.NewRecorder()

	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAppBookingsCanFilterBySince(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	older := now.Add(-10 * time.Second)
	newer := now
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-since-a",
		ExternalBookingID: "ext-since-a",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     older,
		Status:            "pending",
		LastSyncedAt:      &older,
		PetID:             "pet-a",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-since-b",
		ExternalBookingID: "ext-since-b",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     newer,
		Status:            "pending",
		LastSyncedAt:      &newer,
		PetID:             "pet-b",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}
	db.Model(&models.AppBookingMirror{}).Where("id = ?", "mirror-since-a").Update("updated_at", older)
	db.Model(&models.AppBookingMirror{}).Where("id = ?", "mirror-since-b").Update("updated_at", newer)

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?since="+url.QueryEscape(older.Add(5*time.Second).Format(time.RFC3339)), nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].ClinicID != "clinic-2" {
		t.Fatalf("expected clinic-2 booking, got %q", body.Bookings[0].ClinicID)
	}
}

func TestListAppBookingsForceRefreshOverridesFreshness(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	mirror := models.AppBookingMirror{
		ID:                "mirror-force-1",
		ExternalBookingID: "ext-force-1",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	called := false
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		if headers["X-Request-ID"] != "list-force-1" {
			t.Fatalf("expected request id list-force-1 in refresh call, got %q", headers["X-Request-ID"])
		}
		called = true
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-force-1","clinic_integration_id":"clinic-1","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings?force_refresh=true", nil)
	req.Header.Set("X-Request-ID", "list-force-1")
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Fatal("expected force refresh to call gateway")
	}
}

func TestGetAppBookingReturnsRequestIDHeader(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	mirror := models.AppBookingMirror{
		ID:                "mirror-get-1",
		ExternalBookingID: "ext-get-1",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		t.Fatal("did not expect refresh for a fresh get in this test")
		return 0, "", nil, nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-1", nil)
	req.SetPathValue("bookingID", "mirror-get-1")
	req.Header.Set("X-Correlation-ID", "get-corr-1")
	rec := httptest.NewRecorder()
	NewAppBookingDetailHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Request-ID") != "get-corr-1" {
		t.Fatalf("expected response X-Request-ID get-corr-1, got %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestGetAppBookingRejectsInvalidSyncStateFilter(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	mirror := models.AppBookingMirror{
		ID:                "mirror-get-bad",
		ExternalBookingID: "ext-get-bad",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-bad?sync_state=bad", nil)
	req.SetPathValue("bookingID", "mirror-get-bad")
	rec := httptest.NewRecorder()

	NewAppBookingDetailHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetAppBookingReturnsConflictWhenSyncStateDoesNotMatch(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	mirror := models.AppBookingMirror{
		ID:                "mirror-get-conflict",
		ExternalBookingID: "ext-get-conflict",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-conflict?sync_state=stale", nil)
	req.SetPathValue("bookingID", "mirror-get-conflict")
	rec := httptest.NewRecorder()

	NewAppBookingDetailHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetAppBookingAllowsMatchingSyncStateFilter(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	mirror := models.AppBookingMirror{
		ID:                "mirror-get-fresh",
		ExternalBookingID: "ext-get-fresh",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &lastSyncedAt,
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-fresh?sync_state=fresh", nil)
	req.SetPathValue("bookingID", "mirror-get-fresh")
	rec := httptest.NewRecorder()

	NewAppBookingDetailHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetAppBookingReturnsConflictWhenAnchorDoesNotMatch(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	internalID := uint(202)
	mirror := models.AppBookingMirror{
		ID:                            "mirror-get-anchor",
		ExternalBookingID:             "ext-get-anchor",
		ClinicID:                      "clinic-1",
		BookingClinicID:               "clinic_happypaws_hk",
		MerchantInternalAppointmentID: &internalID,
		ClinicName:                    "Clinic One",
		ServiceType:                   "vaccine",
		ScheduledDate:                 time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:                        "pending",
		RequestID:                     "req-anchor",
		LastSyncedAt:                  &lastSyncedAt,
		PetID:                         "pet-123",
		PetName:                       "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-anchor?external_booking_id=wrong", nil)
	req.SetPathValue("bookingID", "mirror-get-anchor")
	rec := httptest.NewRecorder()
	NewAppBookingDetailHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetAppBookingAllowsMatchingAnchorFilters(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	internalID := uint(202)
	mirror := models.AppBookingMirror{
		ID:                            "mirror-get-anchor-ok",
		ExternalBookingID:             "ext-get-anchor-ok",
		ClinicID:                      "clinic-1",
		BookingClinicID:               "clinic_happypaws_hk",
		MerchantInternalAppointmentID: &internalID,
		ClinicName:                    "Clinic One",
		ServiceType:                   "vaccine",
		ScheduledDate:                 time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:                        "pending",
		RequestID:                     "req-anchor-ok",
		LastSyncedAt:                  &lastSyncedAt,
		PetID:                         "pet-123",
		PetName:                       "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-anchor-ok?external_booking_id=ext-get-anchor-ok&request_id=req-anchor-ok&booking_clinic_id=clinic_happypaws_hk&merchant_internal_appointment_id=202", nil)
	req.SetPathValue("bookingID", "mirror-get-anchor-ok")
	rec := httptest.NewRecorder()
	NewAppBookingDetailHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetAppBookingIncludesDebugWhenRequested(t *testing.T) {
	db := newTestDB(t)
	lastSyncedAt := time.Now().UTC()
	internalID := uint(202)
	mirror := models.AppBookingMirror{
		ID:                            "mirror-get-debug",
		ExternalBookingID:             "ext-get-debug",
		ClinicID:                      "clinic-1",
		BookingClinicID:               "clinic_happypaws_hk",
		MerchantInternalAppointmentID: &internalID,
		ClinicName:                    "Clinic One",
		ServiceType:                   "vaccine",
		ScheduledDate:                 time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:                        "pending",
		RequestID:                     "req-get-debug",
		IdempotencyKey:                "idem-get-debug",
		LastSyncedAt:                  &lastSyncedAt,
		PetID:                         "pet-123",
		PetName:                       "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-debug?include_debug=true", nil)
	req.SetPathValue("bookingID", "mirror-get-debug")
	rec := httptest.NewRecorder()
	NewAppBookingDetailHandler(db, fakeGateway{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	data := body["data"].(map[string]any)
	debug := data["debug"].(map[string]any)
	if debug["request_id"] != "req-get-debug" {
		t.Fatalf("expected request_id in debug, got %#v", debug["request_id"])
	}
	if debug["idempotency_key"] != "idem-get-debug" {
		t.Fatalf("expected idempotency_key in debug, got %#v", debug["idempotency_key"])
	}
}

func TestGetAppBookingPassesRequestIDWhenRefreshOccurs(t *testing.T) {
	db := newTestDB(t)
	mirror := models.AppBookingMirror{
		ID:                "mirror-get-refresh-1",
		ExternalBookingID: "ext-get-refresh-1",
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
		if headers["X-Request-ID"] != "get-req-refresh-1" {
			t.Fatalf("expected request id get-req-refresh-1 in refresh call, got %q", headers["X-Request-ID"])
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-get-refresh-1","clinic_integration_id":"clinic-1","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings/mirror-get-refresh-1?force_refresh=true", nil)
	req.SetPathValue("bookingID", "mirror-get-refresh-1")
	req.Header.Set("X-Request-ID", "get-req-refresh-1")
	rec := httptest.NewRecorder()
	NewAppBookingDetailHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Request-ID") != "get-req-refresh-1" {
		t.Fatalf("expected response X-Request-ID get-req-refresh-1, got %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestListAppBookingsMarksSyncErrorOnRefreshFailure(t *testing.T) {
	db := newTestDB(t)
	mirror := models.AppBookingMirror{
		ID:                "mirror-sync-error-1",
		ExternalBookingID: "ext-sync-error-1",
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
		return http.StatusBadGateway, "application/json", []byte(`{"message":"upstream failed"}`), nil
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/bookings", nil)
	rec := httptest.NewRecorder()
	NewAppBookingsHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body appBookingListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Bookings) != 1 {
		t.Fatalf("expected 1 booking, got %d", len(body.Bookings))
	}
	if body.Bookings[0].SyncState != "sync_error" {
		t.Fatalf("expected sync_error state, got %q", body.Bookings[0].SyncState)
	}
	if !body.Bookings[0].IsStale {
		t.Fatal("expected sync_error booking to be stale")
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-sync-error-1").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.LastSyncAttemptAt == nil {
		t.Fatal("expected last sync attempt timestamp after failure")
	}
	if refreshed.LastSyncError != "upstream_status_Bad Gateway" {
		t.Fatalf("expected upstream_status_Bad Gateway, got %q", refreshed.LastSyncError)
	}
}

func TestBookingDTOFromMirrorAtFresh(t *testing.T) {
	now := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)
	lastSyncedAt := now.Add(-30 * time.Second)
	merchantUpdatedAt := now.Add(-1 * time.Minute)
	dto := bookingDTOFromMirrorAt(models.AppBookingMirror{
		ID:                "mirror-fresh-1",
		ClinicID:          "74",
		ClinicName:        "Pawrd Test Clinic",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &lastSyncedAt,
		MerchantUpdatedAt: &merchantUpdatedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}, now)

	if dto.SyncState != "fresh" {
		t.Fatalf("expected fresh sync state, got %q", dto.SyncState)
	}
	if dto.IsStale {
		t.Fatal("expected fresh mirror to not be stale")
	}
	if dto.LastSyncedAt == nil || !dto.LastSyncedAt.Equal(lastSyncedAt) {
		t.Fatalf("expected last_synced_at %v, got %v", lastSyncedAt, dto.LastSyncedAt)
	}
	if dto.MerchantUpdatedAt == nil || !dto.MerchantUpdatedAt.Equal(merchantUpdatedAt) {
		t.Fatalf("expected merchant_updated_at %v, got %v", merchantUpdatedAt, dto.MerchantUpdatedAt)
	}
}

func TestBookingDTOFromMirrorAtStale(t *testing.T) {
	now := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)
	lastSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	dto := bookingDTOFromMirrorAt(models.AppBookingMirror{
		ID:            "mirror-stale-1",
		ClinicID:      "74",
		ClinicName:    "Pawrd Test Clinic",
		ServiceType:   "vaccine",
		ScheduledDate: now,
		Status:        "pending",
		LastSyncedAt:  &lastSyncedAt,
		PetID:         "pet-1",
		PetName:       "Mochi",
	}, now)

	if dto.SyncState != "stale" {
		t.Fatalf("expected stale sync state, got %q", dto.SyncState)
	}
	if !dto.IsStale {
		t.Fatal("expected stale mirror to be stale")
	}
}

func TestBookingDTOFromMirrorAtNeverSynced(t *testing.T) {
	now := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)
	dto := bookingDTOFromMirrorAt(models.AppBookingMirror{
		ID:            "mirror-never-1",
		ClinicID:      "74",
		ClinicName:    "Pawrd Test Clinic",
		ServiceType:   "vaccine",
		ScheduledDate: now,
		Status:        "pending",
		PetID:         "pet-1",
		PetName:       "Mochi",
	}, now)

	if dto.SyncState != "never_synced" {
		t.Fatalf("expected never_synced sync state, got %q", dto.SyncState)
	}
	if !dto.IsStale {
		t.Fatal("expected never synced mirror to be stale")
	}
}

func TestBookingDTOFromMirrorAtSyncError(t *testing.T) {
	now := time.Date(2026, 4, 13, 0, 0, 0, 0, time.UTC)
	lastSyncAttemptAt := now.Add(-10 * time.Second)
	dto := bookingDTOFromMirrorAt(models.AppBookingMirror{
		ID:                "mirror-error-1",
		ClinicID:          "74",
		ClinicName:        "Pawrd Test Clinic",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncAttemptAt: &lastSyncAttemptAt,
		LastSyncError:     "gateway_error",
		PetID:             "pet-1",
		PetName:           "Mochi",
	}, now)

	if dto.SyncState != "sync_error" {
		t.Fatalf("expected sync_error, got %q", dto.SyncState)
	}
	if !dto.IsStale {
		t.Fatal("expected sync_error mirror to be stale")
	}
}

func TestShouldRefreshMirror(t *testing.T) {
	now := time.Now().UTC()
	fresh := models.AppBookingMirror{LastSyncedAt: &now}
	if shouldRefreshMirror(fresh, false) {
		t.Fatal("expected fresh mirror to skip refresh")
	}
	if !shouldRefreshMirror(fresh, true) {
		t.Fatal("expected force refresh to override fresh mirror")
	}
	staleTime := now.Add(-(mirrorFreshnessWindow + time.Second))
	stale := models.AppBookingMirror{LastSyncedAt: &staleTime}
	if !shouldRefreshMirror(stale, false) {
		t.Fatal("expected stale mirror to refresh")
	}
	syncError := models.AppBookingMirror{LastSyncError: "gateway_error"}
	if !shouldRefreshMirror(syncError, false) {
		t.Fatal("expected sync_error mirror to refresh")
	}
	neverSynced := models.AppBookingMirror{}
	if !shouldRefreshMirror(neverSynced, false) {
		t.Fatal("expected never synced mirror to refresh")
	}
}

func TestRequestForcesRefresh(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/bookings?force_refresh=true", nil)
	if !requestForcesRefresh(req) {
		t.Fatal("expected force_refresh=true to be recognized")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/bookings", nil)
	if requestForcesRefresh(req) {
		t.Fatal("expected missing force_refresh to be false")
	}
}

func TestPatchCancelledBookingPreservesOriginalNotes(t *testing.T) {
	db := newTestDB(t)
	lastSyncError := "gateway_error"
	mirror := models.AppBookingMirror{
		ID:                "mirror-cancel-1",
		ExternalBookingID: "ext-cancel-1",
		ClinicID:          "74",
		ClinicName:        "testclinics",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncError:     lastSyncError,
		PetID:             "pet-123",
		PetName:           "Mochi",
		Notes:             "OwnerName: Ada | OriginalNote: retain me",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		if headers["X-Request-ID"] != "cancel-req-1" {
			t.Fatalf("expected cancel request id cancel-req-1, got %q", headers["X-Request-ID"])
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-cancel-1","status":"cancelled_by_user","updated_at":"2026-04-11T12:30:00Z"}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPatch, "/api/bookings/mirror-cancel-1", bytes.NewReader([]byte(`{"status":"cancelled","notes":"cancel reason only"}`)))
	req.SetPathValue("bookingID", "mirror-cancel-1")
	req.Header.Set("X-Request-ID", "cancel-req-1")
	rec := httptest.NewRecorder()
	NewAppBookingDetailHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-cancel-1").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.Notes != "OwnerName: Ada | OriginalNote: retain me" {
		t.Fatalf("expected original notes to be preserved, got %q", refreshed.Notes)
	}
	if refreshed.Status != "cancelled" {
		t.Fatalf("expected cancelled status, got %q", refreshed.Status)
	}
	if refreshed.MerchantStatus != "cancelled" {
		t.Fatalf("expected cancelled merchant status, got %q", refreshed.MerchantStatus)
	}
	if refreshed.LastSyncAttemptAt == nil {
		t.Fatal("expected last sync attempt timestamp")
	}
	if refreshed.LastSyncedAt == nil {
		t.Fatal("expected last synced timestamp")
	}
	if refreshed.LastSyncError != "" {
		t.Fatalf("expected cleared sync error, got %q", refreshed.LastSyncError)
	}
	if refreshed.RequestID != "cancel-req-1" {
		t.Fatalf("expected request id cancel-req-1, got %q", refreshed.RequestID)
	}
	if rec.Header().Get("X-Request-ID") != "cancel-req-1" {
		t.Fatalf("expected response X-Request-ID cancel-req-1, got %q", rec.Header().Get("X-Request-ID"))
	}
	expectedUpdatedAt := time.Date(2026, 4, 11, 12, 30, 0, 0, time.UTC)
	if refreshed.MerchantUpdatedAt == nil || !refreshed.MerchantUpdatedAt.Equal(expectedUpdatedAt) {
		t.Fatalf("expected merchant updated at %s, got %v", expectedUpdatedAt, refreshed.MerchantUpdatedAt)
	}
}

func TestPatchCancelledBookingMarksSyncErrorOnUpstreamFailure(t *testing.T) {
	db := newTestDB(t)
	mirror := models.AppBookingMirror{
		ID:                "mirror-cancel-error-1",
		ExternalBookingID: "ext-cancel-error-1",
		ClinicID:          "74",
		ClinicName:        "testclinics",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		PetID:             "pet-123",
		PetName:           "Mochi",
		Notes:             "OwnerName: Ada | OriginalNote: retain me",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		if headers["X-Request-ID"] != "corr-cancel-2" {
			t.Fatalf("expected cancel request id corr-cancel-2, got %q", headers["X-Request-ID"])
		}
		return http.StatusBadGateway, "application/json", []byte(`{"message":"upstream failed"}`), nil
	}}

	req := httptest.NewRequest(http.MethodPatch, "/api/bookings/mirror-cancel-error-1", bytes.NewReader([]byte(`{"status":"cancelled","notes":"cancel reason only"}`)))
	req.SetPathValue("bookingID", "mirror-cancel-error-1")
	req.Header.Set("X-Correlation-ID", "corr-cancel-2")
	rec := httptest.NewRecorder()
	NewAppBookingDetailHandler(db, gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-cancel-error-1").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.LastSyncAttemptAt == nil {
		t.Fatal("expected last sync attempt timestamp after failure")
	}
	if refreshed.LastSyncError != "upstream_status_Bad Gateway" {
		t.Fatalf("expected upstream_status_Bad Gateway, got %q", refreshed.LastSyncError)
	}
	if refreshed.RequestID != "corr-cancel-2" {
		t.Fatalf("expected request id corr-cancel-2, got %q", refreshed.RequestID)
	}
	if refreshed.Notes != "OwnerName: Ada | OriginalNote: retain me" {
		t.Fatalf("expected notes to remain unchanged, got %q", refreshed.Notes)
	}
}

func TestBookingClinicIDForClinic(t *testing.T) {
	clinic := models.Clinic{ClinicID: "74", Name: "testclinics"}
	if got := bookingClinicIDForClinic(clinic); got != "clinic_happypaws_hk" {
		t.Fatalf("expected clinic_happypaws_hk, got %q", got)
	}
}

func TestBookingClinicIDForPublicClinicID(t *testing.T) {
	if got := bookingClinicIDForPublicClinicID("74"); got != "clinic_happypaws_hk" {
		t.Fatalf("expected clinic_happypaws_hk for 74, got %q", got)
	}
	if got := bookingClinicIDForPublicClinicID("testclinics"); got != "clinic_happypaws_hk" {
		t.Fatalf("expected clinic_happypaws_hk for testclinics, got %q", got)
	}
}

func TestParseMerchantTimestamp(t *testing.T) {
	got := parseMerchantTimestamp("2026-04-11T12:30:00Z")
	if got == nil {
		t.Fatal("expected parsed timestamp")
	}
	expected := time.Date(2026, 4, 11, 12, 30, 0, 0, time.UTC)
	if !got.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected, got)
	}
	if parseMerchantTimestamp("not-a-time") != nil {
		t.Fatal("expected invalid timestamp to return nil")
	}
}

func TestResolveRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/bookings", nil)
	req.Header.Set("X-Request-ID", "req-a")
	if got := resolveRequestID(req); got != "req-a" {
		t.Fatalf("expected req-a, got %q", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/bookings", nil)
	req.Header.Set("X-Correlation-ID", "corr-b")
	if got := resolveRequestID(req); got != "corr-b" {
		t.Fatalf("expected corr-b, got %q", got)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/bookings", nil)
	if got := resolveRequestID(req); got == "" {
		t.Fatal("expected generated request id")
	}
}

func TestAppBookingSyncHandlerRequiresConfiguration(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings/sync", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()

	NewAppBookingSyncHandler(db, "").ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestAppBookingSyncHandlerRejectsInvalidToken(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings/sync", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("X-Booking-Sync-Token", "wrong")
	rec := httptest.NewRecorder()

	NewAppBookingSyncHandler(db, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAppBookingSyncHandlerUpdatesMirror(t *testing.T) {
	db := newTestDB(t)
	mirror := models.AppBookingMirror{
		ID:                "mirror-sync-1",
		ExternalBookingID: "ext-sync-1",
		ClinicID:          "74",
		ClinicName:        "Pawrd Test Clinic",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncError:     "gateway_error",
		PetID:             "pet-123",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/sync", bytes.NewReader([]byte(`{
		"external_booking_id":"ext-sync-1",
		"clinic_integration_id":"clinic_happypaws_hk",
		"clinic_name":"Happy Paws HK",
		"scheduled_at":"2026-04-12T11:00:00Z",
		"status":"confirmed",
		"updated_at":"2026-04-11T12:30:00Z",
		"merchant_status":{"appointment_status":"booked","internal_appointment_id":303}
	}`)))
	req.Header.Set("X-Booking-Sync-Token", "secret")
	req.Header.Set("X-Request-ID", "sync-req-1")
	rec := httptest.NewRecorder()

	NewAppBookingSyncHandler(db, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-sync-1").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.Status != "confirmed" {
		t.Fatalf("expected confirmed, got %q", refreshed.Status)
	}
	if refreshed.MerchantStatus != "booked" {
		t.Fatalf("expected booked merchant status, got %q", refreshed.MerchantStatus)
	}
	if refreshed.MerchantInternalAppointmentID == nil || *refreshed.MerchantInternalAppointmentID != 303 {
		t.Fatalf("expected internal appointment id 303, got %v", refreshed.MerchantInternalAppointmentID)
	}
	if refreshed.BookingClinicID != "clinic_happypaws_hk" {
		t.Fatalf("expected clinic_happypaws_hk, got %q", refreshed.BookingClinicID)
	}
	if refreshed.ClinicName != "Happy Paws HK" {
		t.Fatalf("expected clinic name Happy Paws HK, got %q", refreshed.ClinicName)
	}
	expectedScheduledAt := time.Date(2026, 4, 12, 11, 0, 0, 0, time.UTC)
	if !refreshed.ScheduledDate.Equal(expectedScheduledAt) {
		t.Fatalf("expected scheduled date %s, got %s", expectedScheduledAt, refreshed.ScheduledDate)
	}
	if refreshed.LastSyncAttemptAt == nil || refreshed.LastSyncedAt == nil {
		t.Fatal("expected sync timestamps to be updated")
	}
	if refreshed.LastSyncError != "" {
		t.Fatalf("expected sync error to be cleared, got %q", refreshed.LastSyncError)
	}
	if refreshed.RequestID != "sync-req-1" {
		t.Fatalf("expected request id sync-req-1, got %q", refreshed.RequestID)
	}
	if rec.Header().Get("X-Request-ID") != "sync-req-1" {
		t.Fatalf("expected response X-Request-ID sync-req-1, got %q", rec.Header().Get("X-Request-ID"))
	}
	expectedUpdatedAt := time.Date(2026, 4, 11, 12, 30, 0, 0, time.UTC)
	if refreshed.MerchantUpdatedAt == nil || !refreshed.MerchantUpdatedAt.Equal(expectedUpdatedAt) {
		t.Fatalf("expected merchant updated at %s, got %v", expectedUpdatedAt, refreshed.MerchantUpdatedAt)
	}
}

func TestAppBookingReconcileHandlerRequiresConfiguration(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale", nil)
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, fakeGateway{}, "").ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestAppBookingReconcileHandlerRejectsInvalidToken(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale", nil)
	req.Header.Set("X-Booking-Sync-Token", "wrong")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, fakeGateway{}, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAppBookingReconcileHandlerRefreshesEligibleMirrorsOnly(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	freshSyncedAt := now
	stale := models.AppBookingMirror{
		ID:                "mirror-reconcile-stale",
		ExternalBookingID: "ext-reconcile-stale",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	fresh := models.AppBookingMirror{
		ID:                "mirror-reconcile-fresh",
		ExternalBookingID: "ext-reconcile-fresh",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &freshSyncedAt,
		PetID:             "pet-2",
		PetName:           "Latte",
	}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatalf("seed stale mirror: %v", err)
	}
	if err := db.Create(&fresh).Error; err != nil {
		t.Fatalf("seed fresh mirror: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		if headers["X-Request-ID"] != "reconcile-req-1" {
			t.Fatalf("expected request id reconcile-req-1, got %q", headers["X-Request-ID"])
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-reconcile-stale","clinic_integration_id":"clinic-1","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?limit=10", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	req.Header.Set("X-Request-ID", "reconcile-req-1")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 refresh call, got %d", calls)
	}
	if rec.Header().Get("X-Request-ID") != "reconcile-req-1" {
		t.Fatalf("expected response X-Request-ID reconcile-req-1, got %q", rec.Header().Get("X-Request-ID"))
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	counts := data["counts_by_sync_state"].(map[string]any)
	if int(counts["stale"].(float64)) != 1 {
		t.Fatalf("expected stale count 1, got %#v", counts["stale"])
	}
	if int(counts["fresh"].(float64)) != 1 {
		t.Fatalf("expected fresh count 1, got %#v", counts["fresh"])
	}

	var refreshedStale models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-reconcile-stale").First(&refreshedStale).Error; err != nil {
		t.Fatalf("reload stale mirror: %v", err)
	}
	if refreshedStale.Status != "confirmed" {
		t.Fatalf("expected confirmed stale mirror, got %q", refreshedStale.Status)
	}
	if refreshedStale.RequestID != "reconcile-req-1" {
		t.Fatalf("expected reconcile request id on stale mirror, got %q", refreshedStale.RequestID)
	}
	if refreshedStale.LastSyncSource != "reconcile_refresh" {
		t.Fatalf("expected reconcile_refresh sync source, got %q", refreshedStale.LastSyncSource)
	}

	var refreshedFresh models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-reconcile-fresh").First(&refreshedFresh).Error; err != nil {
		t.Fatalf("reload fresh mirror: %v", err)
	}
	if refreshedFresh.Status != "pending" {
		t.Fatalf("expected fresh mirror to remain pending, got %q", refreshedFresh.Status)
	}
}

func TestAppBookingReconcileHandlerCanTargetSingleBooking(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-target-a",
		ExternalBookingID: "ext-target-a",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-target-b",
		ExternalBookingID: "ext-target-b",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-2",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		if path != "/app/v1/vaccinations/bookings/ext-target-b" {
			t.Fatalf("expected only ext-target-b to refresh, got %s", path)
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-target-b","clinic_integration_id":"clinic-2","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?external_booking_id=ext-target-b", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	req.Header.Set("X-Request-ID", "reconcile-target-1")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 targeted refresh call, got %d", calls)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	counts := data["counts_by_sync_state"].(map[string]any)
	if int(counts["stale"].(float64)) != 1 {
		t.Fatalf("expected stale count 1, got %#v", counts["stale"])
	}
	if int(counts["fresh"].(float64)) != 0 {
		t.Fatalf("expected fresh count 0, got %#v", counts["fresh"])
	}
	if data["external_booking_id"] != "ext-target-b" {
		t.Fatalf("expected external_booking_id ext-target-b, got %#v", data["external_booking_id"])
	}
	if int(data["scanned_count"].(float64)) != 1 {
		t.Fatalf("expected scanned_count 1, got %#v", data["scanned_count"])
	}
	if int(data["refreshed_count"].(float64)) != 1 {
		t.Fatalf("expected refreshed_count 1, got %#v", data["refreshed_count"])
	}
	var refreshedTarget models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-target-b").First(&refreshedTarget).Error; err != nil {
		t.Fatalf("reload targeted mirror: %v", err)
	}
	if refreshedTarget.LastSyncSource != "reconcile_refresh" {
		t.Fatalf("expected reconcile_refresh sync source, got %q", refreshedTarget.LastSyncSource)
	}
}

func TestAppBookingReconcileHandlerReturnsNotFoundForMissingTarget(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?external_booking_id=ext-missing", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, fakeGateway{}, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAppBookingReconcileHandlerDryRunReportsEligibleWithoutRefreshing(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	mirror := models.AppBookingMirror{
		ID:                "mirror-dry-run-1",
		ExternalBookingID: "ext-dry-run-1",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		return http.StatusOK, "application/json", []byte(`{}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?dry_run=true", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	req.Header.Set("X-Request-ID", "reconcile-dry-run-1")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("expected no refresh calls in dry run, got %d", calls)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	if data["dry_run"] != true {
		t.Fatalf("expected dry_run true, got %#v", data["dry_run"])
	}
	if int(data["eligible_count"].(float64)) != 1 {
		t.Fatalf("expected eligible_count 1, got %#v", data["eligible_count"])
	}
	if int(data["refreshed_count"].(float64)) != 0 {
		t.Fatalf("expected refreshed_count 0, got %#v", data["refreshed_count"])
	}
	ids := data["eligible_external_booking_ids"].([]any)
	if len(ids) != 1 || ids[0].(string) != "ext-dry-run-1" {
		t.Fatalf("expected eligible id ext-dry-run-1, got %#v", ids)
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-dry-run-1").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.LastSyncSource != "" {
		t.Fatalf("expected dry run not to mutate mirror, got last_sync_source=%q", refreshed.LastSyncSource)
	}
}

func TestAppBookingReconcileHandlerIncludesDebugResultsWhenRequested(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	mirror := models.AppBookingMirror{
		ID:                "mirror-reconcile-debug",
		ExternalBookingID: "ext-reconcile-debug",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		LastSyncedAt:      &staleSyncedAt,
		RequestID:         "old-req",
		LastSyncSource:    "",
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?dry_run=true&include_debug=true", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, fakeGateway{}, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	results := data["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("expected 1 debug result, got %d", len(results))
	}
	result := results[0].(map[string]any)
	if result["external_booking_id"] != "ext-reconcile-debug" {
		t.Fatalf("expected ext-reconcile-debug, got %#v", result["external_booking_id"])
	}
	if result["action"] != "eligible_dry_run" {
		t.Fatalf("expected eligible_dry_run, got %#v", result["action"])
	}
	if result["sync_state"] != "stale" {
		t.Fatalf("expected stale sync_state, got %#v", result["sync_state"])
	}
}

func TestAppBookingReconcileHandlerRejectsInvalidSyncState(t *testing.T) {
	db := newTestDB(t)
	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?sync_state=bad", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, fakeGateway{}, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAppBookingReconcileHandlerCanFilterBySyncState(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	stale := models.AppBookingMirror{
		ID:                "mirror-filter-stale",
		ExternalBookingID: "ext-filter-stale",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	syncErr := models.AppBookingMirror{
		ID:                "mirror-filter-error",
		ExternalBookingID: "ext-filter-error",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncError:     "gateway_error",
		PetID:             "pet-2",
		PetName:           "Latte",
	}
	if err := db.Create(&stale).Error; err != nil {
		t.Fatalf("seed stale mirror: %v", err)
	}
	if err := db.Create(&syncErr).Error; err != nil {
		t.Fatalf("seed syncErr mirror: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		if path != "/app/v1/vaccinations/bookings/ext-filter-error" {
			t.Fatalf("expected only ext-filter-error to refresh, got %s", path)
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-filter-error","clinic_integration_id":"clinic-2","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?sync_state=sync_error", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 refresh call, got %d", calls)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	if data["sync_state"] != "sync_error" {
		t.Fatalf("expected sync_state sync_error, got %#v", data["sync_state"])
	}
	if int(data["eligible_count"].(float64)) != 1 {
		t.Fatalf("expected eligible_count 1, got %#v", data["eligible_count"])
	}
	ids := data["eligible_external_booking_ids"].([]any)
	if len(ids) != 1 || ids[0].(string) != "ext-filter-error" {
		t.Fatalf("expected eligible ext-filter-error, got %#v", ids)
	}
	skipped := data["skipped_external_booking_ids"].([]any)
	if len(skipped) != 1 || skipped[0].(string) != "ext-filter-stale" {
		t.Fatalf("expected skipped ext-filter-stale, got %#v", skipped)
	}
}

func TestAppBookingReconcileHandlerCanFilterByPetID(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-pet-reconcile-a",
		ExternalBookingID: "ext-pet-reconcile-a",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-a",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-pet-reconcile-b",
		ExternalBookingID: "ext-pet-reconcile-b",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-b",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		if path != "/app/v1/vaccinations/bookings/ext-pet-reconcile-b" {
			t.Fatalf("expected only ext-pet-reconcile-b to refresh, got %s", path)
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-pet-reconcile-b","clinic_integration_id":"clinic-2","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?pet_id=pet-b", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 refresh call, got %d", calls)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	if data["pet_id"] != "pet-b" {
		t.Fatalf("expected pet_id pet-b, got %#v", data["pet_id"])
	}
	if int(data["scanned_count"].(float64)) != 1 {
		t.Fatalf("expected scanned_count 1, got %#v", data["scanned_count"])
	}
}

func TestAppBookingReconcileHandlerCanFilterByClinicID(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-clinic-reconcile-a",
		ExternalBookingID: "ext-clinic-reconcile-a",
		ClinicID:          "clinic-a",
		ClinicName:        "Clinic A",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-a",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-clinic-reconcile-b",
		ExternalBookingID: "ext-clinic-reconcile-b",
		ClinicID:          "clinic-b",
		ClinicName:        "Clinic B",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-b",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		if path != "/app/v1/vaccinations/bookings/ext-clinic-reconcile-b" {
			t.Fatalf("expected only ext-clinic-reconcile-b to refresh, got %s", path)
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-clinic-reconcile-b","clinic_integration_id":"clinic-b","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?clinic_id=clinic-b", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()
	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 refresh call, got %d", calls)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	data := body["data"].(map[string]any)
	if data["clinic_id"] != "clinic-b" {
		t.Fatalf("expected clinic_id clinic-b, got %#v", data["clinic_id"])
	}
	if int(data["scanned_count"].(float64)) != 1 {
		t.Fatalf("expected scanned_count 1, got %#v", data["scanned_count"])
	}
}

func TestAppBookingReconcileHandlerCanFilterByRequestID(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	staleSyncedAt := now.Add(-(mirrorFreshnessWindow + time.Second))
	mirrorA := models.AppBookingMirror{
		ID:                "mirror-req-reconcile-a",
		ExternalBookingID: "ext-req-reconcile-a",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		RequestID:         "req-a",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-a",
		PetName:           "Mochi",
	}
	mirrorB := models.AppBookingMirror{
		ID:                "mirror-req-reconcile-b",
		ExternalBookingID: "ext-req-reconcile-b",
		ClinicID:          "clinic-2",
		ClinicName:        "Clinic Two",
		ServiceType:       "vaccine",
		ScheduledDate:     now,
		Status:            "pending",
		RequestID:         "req-b",
		LastSyncedAt:      &staleSyncedAt,
		PetID:             "pet-b",
		PetName:           "Latte",
	}
	if err := db.Create(&mirrorA).Error; err != nil {
		t.Fatalf("seed mirrorA: %v", err)
	}
	if err := db.Create(&mirrorB).Error; err != nil {
		t.Fatalf("seed mirrorB: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		if path != "/app/v1/vaccinations/bookings/ext-req-reconcile-b" {
			t.Fatalf("expected only ext-req-reconcile-b to refresh, got %s", path)
		}
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-req-reconcile-b","clinic_integration_id":"clinic-2","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?request_id=req-b", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 refresh call, got %d", calls)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	if data["request_id"] != "req-b" {
		t.Fatalf("expected request_id req-b, got %#v", data["request_id"])
	}
	if int(data["scanned_count"].(float64)) != 1 {
		t.Fatalf("expected scanned_count 1, got %#v", data["scanned_count"])
	}
}

func TestAppBookingReconcileHandlerCanForceRefreshFreshMirror(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	freshSyncedAt := now
	mirror := models.AppBookingMirror{
		ID:                "mirror-force-refresh",
		ExternalBookingID: "ext-force-refresh",
		ClinicID:          "clinic-1",
		ClinicName:        "Clinic One",
		ServiceType:       "vaccine",
		ScheduledDate:     time.Date(2026, 4, 12, 10, 0, 0, 0, time.UTC),
		Status:            "pending",
		MerchantStatus:    "pending",
		LastSyncedAt:      &freshSyncedAt,
		PetID:             "pet-1",
		PetName:           "Mochi",
	}
	if err := db.Create(&mirror).Error; err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	calls := 0
	gateway := fakeGateway{send: func(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
		calls++
		return http.StatusOK, "application/json", []byte(`{"code":0,"message":"ok","data":{"external_booking_id":"ext-force-refresh","clinic_integration_id":"clinic-1","status":"confirmed","scheduled_at":"2026-04-12T10:00:00Z","updated_at":"2026-04-11T12:30:00Z","merchant_status":{"appointment_status":"booked"}}}`), nil
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/reconcile-stale?force=true", nil)
	req.Header.Set("X-Booking-Sync-Token", "secret")
	rec := httptest.NewRecorder()

	NewAppBookingReconcileHandler(db, gateway, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("expected 1 forced refresh call, got %d", calls)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := body["data"].(map[string]any)
	if data["force"] != true {
		t.Fatalf("expected force=true, got %#v", data["force"])
	}
	if int(data["refreshed_count"].(float64)) != 1 {
		t.Fatalf("expected refreshed_count 1, got %#v", data["refreshed_count"])
	}
}

func TestAppBookingSyncHandlerFallsBackToCorrelationID(t *testing.T) {
	db := newTestDB(t)
	mirror := models.AppBookingMirror{
		ID:                "mirror-sync-2",
		ExternalBookingID: "ext-sync-2",
		ClinicID:          "74",
		ClinicName:        "Pawrd Test Clinic",
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

	req := httptest.NewRequest(http.MethodPost, "/api/bookings/sync", bytes.NewReader([]byte(`{
		"external_booking_id":"ext-sync-2",
		"status":"confirmed"
	}`)))
	req.Header.Set("X-Booking-Sync-Token", "secret")
	req.Header.Set("X-Correlation-ID", "corr-sync-2")
	rec := httptest.NewRecorder()

	NewAppBookingSyncHandler(db, "secret").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var refreshed models.AppBookingMirror
	if err := db.Where("id = ?", "mirror-sync-2").First(&refreshed).Error; err != nil {
		t.Fatalf("reload mirror: %v", err)
	}
	if refreshed.RequestID != "corr-sync-2" {
		t.Fatalf("expected corr-sync-2, got %q", refreshed.RequestID)
	}
	if rec.Header().Get("X-Request-ID") != "corr-sync-2" {
		t.Fatalf("expected response X-Request-ID corr-sync-2, got %q", rec.Header().Get("X-Request-ID"))
	}
}
