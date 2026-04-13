package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/merchant"
)

type fakeVaccinationGateway struct {
	method      string
	path        string
	query       url.Values
	body        []byte
	headers     map[string]string
	statusCode  int
	contentType string
	response    []byte
	err         error
}

func (f *fakeVaccinationGateway) Send(_ context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error) {
	f.method = method
	f.path = path
	f.query = query
	f.body = append([]byte(nil), body...)
	f.headers = headers
	return f.statusCode, f.contentType, append([]byte(nil), f.response...), f.err
}

func TestVaccinationAvailabilityProxyForwardsRequest(t *testing.T) {
	gateway := &fakeVaccinationGateway{
		statusCode:  http.StatusOK,
		contentType: "application/json",
		response:    []byte(`{"code":0,"message":"ok"}`),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/medical/vaccinations/availability?clinic_integration_id=clinic_testclinics_hk&vaccine_code=rabies&date=2026-04-11", nil)
	rec := httptest.NewRecorder()

	NewVaccinationAvailabilityProxyHandler(gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if gateway.method != http.MethodGet {
		t.Fatalf("expected GET, got %s", gateway.method)
	}
	if gateway.path != "/app/v1/vaccinations/availability" {
		t.Fatalf("unexpected path: %s", gateway.path)
	}
	if got := gateway.query.Get("clinic_integration_id"); got != "clinic_testclinics_hk" {
		t.Fatalf("unexpected clinic_integration_id: %s", got)
	}
	if body := rec.Body.String(); body != `{"code":0,"message":"ok"}` {
		t.Fatalf("unexpected response body: %s", body)
	}
}

func TestVaccinationBookingCreateGeneratesIdempotencyKey(t *testing.T) {
	gateway := &fakeVaccinationGateway{
		statusCode:  http.StatusOK,
		contentType: "application/json",
		response:    []byte(`{"code":0}`),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/medical/vaccinations/bookings", strings.NewReader(`{"clinic_integration_id":"clinic_testclinics_hk"}`))
	rec := httptest.NewRecorder()

	NewVaccinationBookingCreateProxyHandler(gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if gateway.headers["Idempotency-Key"] == "" {
		t.Fatal("expected generated idempotency key")
	}
	if rec.Header().Get("X-Idempotency-Key") == "" {
		t.Fatal("expected response idempotency header")
	}
	if gateway.headers["Content-Type"] != "application/json" {
		t.Fatalf("expected json content type, got %q", gateway.headers["Content-Type"])
	}
}

func TestVaccinationBookingGetReturnsServiceUnavailableWhenGatewayMissing(t *testing.T) {
	gateway := &fakeVaccinationGateway{err: merchant.ErrNotConfigured}
	req := httptest.NewRequest(http.MethodGet, "/api/medical/vaccinations/bookings/ext-123", nil)
	req.SetPathValue("externalBookingID", "ext-123")
	rec := httptest.NewRecorder()

	NewVaccinationBookingGetProxyHandler(gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not configured") {
		t.Fatalf("unexpected response body: %s", rec.Body.String())
	}
}

func TestVaccinationBookingCancelRejectsInvalidJSON(t *testing.T) {
	gateway := &fakeVaccinationGateway{err: errors.New("should not be called")}
	req := httptest.NewRequest(http.MethodPost, "/api/medical/vaccinations/bookings/ext-123/cancel", strings.NewReader(`{`))
	req.SetPathValue("externalBookingID", "ext-123")
	rec := httptest.NewRecorder()

	NewVaccinationBookingCancelProxyHandler(gateway).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}
