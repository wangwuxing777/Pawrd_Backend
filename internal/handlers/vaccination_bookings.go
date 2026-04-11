package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/merchant"
)

type VaccinationFacadeGateway interface {
	Send(ctx context.Context, method, path string, query url.Values, body []byte, headers map[string]string) (int, string, []byte, error)
}

const merchantVaccinationBasePath = "/app/v1/vaccinations"

func NewVaccinationAvailabilityProxyHandler(gateway VaccinationFacadeGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		query := cloneValues(r.URL.Query())
		for _, key := range []string{"clinic_integration_id", "vaccine_code", "date"} {
			if strings.TrimSpace(query.Get(key)) == "" {
				http.Error(w, key+" is required", http.StatusBadRequest)
				return
			}
		}

		proxyMerchantVaccination(w, r, gateway, http.MethodGet, merchantVaccinationBasePath+"/availability", query, nil, nil)
	}
}

func NewVaccinationBookingCreateProxyHandler(gateway VaccinationFacadeGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := readJSONBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		idempotencyKey := strings.TrimSpace(r.Header.Get("X-Idempotency-Key"))
		if idempotencyKey == "" {
			idempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		}
		if idempotencyKey == "" {
			idempotencyKey = uuid.NewString()
		}

		headers := map[string]string{
			"Content-Type":    "application/json",
			"Idempotency-Key": idempotencyKey,
		}
		w.Header().Set("X-Idempotency-Key", idempotencyKey)
		proxyMerchantVaccination(w, r, gateway, http.MethodPost, merchantVaccinationBasePath+"/bookings", nil, body, headers)
	}
}

func NewVaccinationBookingGetProxyHandler(gateway VaccinationFacadeGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		externalBookingID := strings.TrimSpace(r.PathValue("externalBookingID"))
		if externalBookingID == "" {
			http.Error(w, "externalBookingID is required", http.StatusBadRequest)
			return
		}

		proxyMerchantVaccination(w, r, gateway, http.MethodGet, merchantVaccinationBasePath+"/bookings/"+url.PathEscape(externalBookingID), nil, nil, nil)
	}
}

func NewVaccinationBookingCancelProxyHandler(gateway VaccinationFacadeGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		externalBookingID := strings.TrimSpace(r.PathValue("externalBookingID"))
		if externalBookingID == "" {
			http.Error(w, "externalBookingID is required", http.StatusBadRequest)
			return
		}

		body, err := readJSONBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		headers := map[string]string{"Content-Type": "application/json"}
		proxyMerchantVaccination(w, r, gateway, http.MethodPost, merchantVaccinationBasePath+"/bookings/"+url.PathEscape(externalBookingID)+"/cancel", nil, body, headers)
	}
}

func proxyMerchantVaccination(w http.ResponseWriter, r *http.Request, gateway VaccinationFacadeGateway, method, path string, query url.Values, body []byte, headers map[string]string) {
	statusCode, contentType, responseBody, err := gateway.Send(r.Context(), method, path, query, body, headers)
	if err != nil {
		if errors.Is(err, merchant.ErrNotConfigured) {
			http.Error(w, "Merchant vaccination gateway is not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "Failed to contact merchant vaccination gateway", http.StatusBadGateway)
		return
	}

	if strings.TrimSpace(contentType) != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(responseBody)
}

func readJSONBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return []byte("{}"), nil
	}
	if !json.Valid(body) {
		return nil, errors.New("invalid request body")
	}
	return body, nil
}

func cloneValues(values url.Values) url.Values {
	out := make(url.Values, len(values))
	for key, vals := range values {
		out[key] = append([]string(nil), vals...)
	}
	return out
}
