package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/merchant"
	"gorm.io/gorm"
)

type appBookingRequest struct {
	ClinicID      string    `json:"clinic_id"`
	PetID         string    `json:"pet_id"`
	ServiceType   string    `json:"service_type"`
	ScheduledDate time.Time `json:"scheduled_date"`
	Notes         string    `json:"notes"`
}

type appBookingPatchRequest struct {
	Status string `json:"status"`
	Notes  string `json:"notes"`
}

type appBookingDTO struct {
	ID            string    `json:"id"`
	ClinicID      string    `json:"clinic_id"`
	ClinicName    string    `json:"clinic_name"`
	ServiceType   string    `json:"service_type"`
	ScheduledDate time.Time `json:"scheduled_date"`
	Status        string    `json:"status"`
	Notes         string    `json:"notes,omitempty"`
	PetID         string    `json:"pet_id"`
	PetName       string    `json:"pet_name"`
	CreatedAt     time.Time `json:"created_at"`
}

type appBookingListResponse struct {
	Bookings []appBookingDTO `json:"bookings"`
}

type appBookingCreateEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	} `json:"data"`
}

type merchantCreateBookingRequest struct {
	ClinicIntegrationID string `json:"clinic_integration_id"`
	VaccineCode         string `json:"vaccine_code"`
	ScheduledAt         string `json:"scheduled_at"`
	Pet                 struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"pet"`
	Owner struct {
		Name  string `json:"name"`
		Phone string `json:"phone"`
		Email string `json:"email,omitempty"`
	} `json:"owner"`
	Notes string `json:"notes,omitempty"`
}

type merchantBookingEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ExternalBookingID   string `json:"external_booking_id"`
		ClinicIntegrationID string `json:"clinic_integration_id"`
		Status              string `json:"status"`
		ScheduledAt         string `json:"scheduled_at"`
		UpdatedAt           string `json:"updated_at"`
		CreatedAt           string `json:"created_at"`
		MerchantStatus      struct {
			AppointmentStatus string `json:"appointment_status"`
		} `json:"merchant_status"`
	} `json:"data"`
}

var clinicNameCache = struct {
	once sync.Once
	data map[string]string
}{}

func NewAppBookingsHandler(db *gorm.DB, gateway VaccinationFacadeGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		switch r.Method {
		case http.MethodGet:
			handleListAppBookings(w, r, db, gateway)
		case http.MethodPost:
			handleCreateAppBooking(w, r, db, gateway)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func NewAppBookingDetailHandler(db *gorm.DB, gateway VaccinationFacadeGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		switch r.Method {
		case http.MethodGet:
			handleGetAppBooking(w, r, db, gateway)
		case http.MethodPatch:
			handlePatchAppBooking(w, r, db, gateway)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func handleCreateAppBooking(w http.ResponseWriter, r *http.Request, db *gorm.DB, gateway VaccinationFacadeGateway) {
	var req appBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.ClinicID = strings.TrimSpace(req.ClinicID)
	req.PetID = strings.TrimSpace(req.PetID)
	req.ServiceType = strings.TrimSpace(strings.ToLower(req.ServiceType))
	req.Notes = strings.TrimSpace(req.Notes)

	if req.ClinicID == "" || req.PetID == "" || req.ServiceType == "" || req.ScheduledDate.IsZero() {
		http.Error(w, "clinic_id, pet_id, service_type and scheduled_date are required", http.StatusBadRequest)
		return
	}
	if req.ServiceType != "vaccine" {
		http.Error(w, "only vaccine bookings are currently supported", http.StatusUnprocessableEntity)
		return
	}

	ownerName, ownerEmail, ownerPhone := parseOwnerDetails(req.Notes)
	if ownerName == "" || ownerPhone == "" {
		http.Error(w, "booking notes must include owner contact details", http.StatusBadRequest)
		return
	}

	merchantReq := merchantCreateBookingRequest{
		ClinicIntegrationID: req.ClinicID,
		VaccineCode:         req.ServiceType,
		ScheduledAt:         req.ScheduledDate.UTC().Format(time.RFC3339),
		Notes:               req.Notes,
	}
	merchantReq.Pet.ID = req.PetID
	merchantReq.Pet.Name = inferPetName(req.PetID, req.Notes)
	merchantReq.Owner.Name = ownerName
	merchantReq.Owner.Email = ownerEmail
	merchantReq.Owner.Phone = ownerPhone

	payload, err := json.Marshal(merchantReq)
	if err != nil {
		http.Error(w, "Failed to build merchant request", http.StatusInternalServerError)
		return
	}

	idempotencyKey := uuid.NewString()
	statusCode, _, responseBody, err := gateway.Send(r.Context(), http.MethodPost, "/app/v1/vaccinations/bookings", nil, payload, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": idempotencyKey,
	})
	if err != nil {
		if errors.Is(err, merchant.ErrNotConfigured) {
			http.Error(w, "Merchant vaccination gateway is not configured", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "Failed to contact merchant vaccination gateway", http.StatusBadGateway)
		return
	}
	if statusCode < 200 || statusCode > 299 {
		writeRawJSONOrFallback(w, statusCode, responseBody)
		return
	}

	var merchantResp merchantBookingEnvelope
	if err := json.Unmarshal(responseBody, &merchantResp); err != nil {
		http.Error(w, "Failed to decode merchant response", http.StatusBadGateway)
		return
	}

	mirror := models.AppBookingMirror{
		ExternalBookingID: merchantResp.Data.ExternalBookingID,
		ClinicID:          req.ClinicID,
		ClinicName:        lookupClinicName(req.ClinicID),
		ServiceType:       req.ServiceType,
		ScheduledDate:     req.ScheduledDate.UTC(),
		Status:            mapMerchantStatusToAppStatus(merchantResp.Data.Status),
		MerchantStatus:    merchantResp.Data.MerchantStatus.AppointmentStatus,
		Notes:             req.Notes,
		PetID:             req.PetID,
		PetName:           merchantReq.Pet.Name,
	}
	if mirror.ClinicName == "" {
		mirror.ClinicName = req.ClinicID
	}
	if err := db.Create(&mirror).Error; err != nil {
		http.Error(w, "Failed to persist booking mirror", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(appBookingCreateEnvelope{
		Success: true,
		Data: struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Message string `json:"message,omitempty"`
		}{
			ID:      mirror.ID,
			Status:  mirror.Status,
			Message: "Booking created via Pawrd Backend",
		},
	})
}

func handleListAppBookings(w http.ResponseWriter, r *http.Request, db *gorm.DB, gateway VaccinationFacadeGateway) {
	var mirrors []models.AppBookingMirror
	query := db.Order("scheduled_date DESC")
	if status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status"))); status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Find(&mirrors).Error; err != nil {
		http.Error(w, "Failed to fetch bookings", http.StatusInternalServerError)
		return
	}

	for i := range mirrors {
		refreshMirrorFromMerchant(r.Context(), db, gateway, &mirrors[i])
	}

	bookings := make([]appBookingDTO, 0, len(mirrors))
	for _, mirror := range mirrors {
		bookings = append(bookings, bookingDTOFromMirror(mirror))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(appBookingListResponse{Bookings: bookings})
}

func handleGetAppBooking(w http.ResponseWriter, r *http.Request, db *gorm.DB, gateway VaccinationFacadeGateway) {
	bookingID := strings.TrimSpace(r.PathValue("bookingID"))
	if bookingID == "" {
		http.Error(w, "bookingID is required", http.StatusBadRequest)
		return
	}

	var mirror models.AppBookingMirror
	if err := db.Where("id = ?", bookingID).First(&mirror).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Booking not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to fetch booking", http.StatusInternalServerError)
		return
	}
	refreshMirrorFromMerchant(r.Context(), db, gateway, &mirror)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    bookingDTOFromMirror(mirror),
	})
}

func handlePatchAppBooking(w http.ResponseWriter, r *http.Request, db *gorm.DB, gateway VaccinationFacadeGateway) {
	bookingID := strings.TrimSpace(r.PathValue("bookingID"))
	if bookingID == "" {
		http.Error(w, "bookingID is required", http.StatusBadRequest)
		return
	}

	var req appBookingPatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	var mirror models.AppBookingMirror
	if err := db.Where("id = ?", bookingID).First(&mirror).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Booking not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to fetch booking", http.StatusInternalServerError)
		return
	}

	desiredStatus := strings.TrimSpace(strings.ToLower(req.Status))
	if desiredStatus != "" && desiredStatus != "cancelled" {
		http.Error(w, "only cancelled status updates are currently supported", http.StatusUnprocessableEntity)
		return
	}

	if desiredStatus == "cancelled" {
		payload, _ := json.Marshal(map[string]string{"reason": strings.TrimSpace(req.Notes)})
		statusCode, _, responseBody, err := gateway.Send(r.Context(), http.MethodPost, "/app/v1/vaccinations/bookings/"+url.PathEscape(mirror.ExternalBookingID)+"/cancel", nil, payload, map[string]string{
			"Content-Type": "application/json",
		})
		if err != nil {
			if errors.Is(err, merchant.ErrNotConfigured) {
				http.Error(w, "Merchant vaccination gateway is not configured", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "Failed to contact merchant vaccination gateway", http.StatusBadGateway)
			return
		}
		if statusCode < 200 || statusCode > 299 {
			writeRawJSONOrFallback(w, statusCode, responseBody)
			return
		}
		mirror.Status = "cancelled"
		mirror.MerchantStatus = "cancelled"
	}

	if strings.TrimSpace(req.Notes) != "" {
		mirror.Notes = strings.TrimSpace(req.Notes)
	}
	if err := db.Save(&mirror).Error; err != nil {
		http.Error(w, "Failed to update booking mirror", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"id":         mirror.ID,
			"status":     mirror.Status,
			"updated_at": mirror.UpdatedAt,
		},
	})
}

func refreshMirrorFromMerchant(ctx context.Context, db *gorm.DB, gateway VaccinationFacadeGateway, mirror *models.AppBookingMirror) {
	if mirror.ExternalBookingID == "" {
		return
	}
	statusCode, _, responseBody, err := gateway.Send(ctx, http.MethodGet, "/app/v1/vaccinations/bookings/"+url.PathEscape(mirror.ExternalBookingID), nil, nil, nil)
	if err != nil || statusCode < 200 || statusCode > 299 {
		return
	}

	var merchantResp merchantBookingEnvelope
	if err := json.Unmarshal(responseBody, &merchantResp); err != nil {
		return
	}

	updated := false
	if scheduledAt, err := time.Parse(time.RFC3339, merchantResp.Data.ScheduledAt); err == nil && !scheduledAt.Equal(mirror.ScheduledDate) {
		mirror.ScheduledDate = scheduledAt.UTC()
		updated = true
	}
	nextStatus := mapMerchantStatusToAppStatus(merchantResp.Data.Status)
	if nextStatus != "" && nextStatus != mirror.Status {
		mirror.Status = nextStatus
		updated = true
	}
	if merchantResp.Data.MerchantStatus.AppointmentStatus != "" && merchantResp.Data.MerchantStatus.AppointmentStatus != mirror.MerchantStatus {
		mirror.MerchantStatus = merchantResp.Data.MerchantStatus.AppointmentStatus
		updated = true
	}
	if updated {
		_ = db.Save(mirror).Error
	}
}

func bookingDTOFromMirror(mirror models.AppBookingMirror) appBookingDTO {
	return appBookingDTO{
		ID:            mirror.ID,
		ClinicID:      mirror.ClinicID,
		ClinicName:    mirror.ClinicName,
		ServiceType:   mirror.ServiceType,
		ScheduledDate: mirror.ScheduledDate,
		Status:        mirror.Status,
		Notes:         mirror.Notes,
		PetID:         mirror.PetID,
		PetName:       mirror.PetName,
		CreatedAt:     mirror.CreatedAt,
	}
}

func parseOwnerDetails(notes string) (name, email, phone string) {
	parts := strings.Split(notes, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "OwnerName:"):
			name = strings.TrimSpace(strings.TrimPrefix(part, "OwnerName:"))
		case strings.HasPrefix(part, "OwnerEmail:"):
			email = strings.TrimSpace(strings.TrimPrefix(part, "OwnerEmail:"))
		case strings.HasPrefix(part, "OwnerPhone:"):
			phone = strings.TrimSpace(strings.TrimPrefix(part, "OwnerPhone:"))
		}
	}
	return
}

func inferPetName(petID, notes string) string {
	for _, part := range strings.Split(notes, "|") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "Pet:") {
			petName := strings.TrimSpace(strings.TrimPrefix(part, "Pet:"))
			if petName != "" {
				return petName
			}
		}
	}
	return petID
}

func mapMerchantStatusToAppStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "requested", "pending":
		return "pending"
	case "confirmed", "booked", "upcoming":
		return "confirmed"
	case "completed":
		return "completed"
	case "cancelled", "cancelled_by_user":
		return "cancelled"
	default:
		return "pending"
	}
}

func lookupClinicName(clinicID string) string {
	clinicNameCache.once.Do(func() {
		clinicNameCache.data = make(map[string]string)
		paths := []string{
			filepath.Join("assets", "clinics.csv"),
			filepath.Join("..", "assets", "clinics.csv"),
		}
		for _, path := range paths {
			file, err := os.Open(path)
			if err != nil {
				continue
			}
			defer file.Close()
			rows, err := csv.NewReader(file).ReadAll()
			if err != nil || len(rows) == 0 {
				return
			}
			header := rows[0]
			clinicIDIdx, nameIdx := -1, -1
			for i, column := range header {
				switch column {
				case "clinic_id":
					clinicIDIdx = i
				case "name":
					nameIdx = i
				}
			}
			if clinicIDIdx < 0 || nameIdx < 0 {
				return
			}
			for _, row := range rows[1:] {
				if clinicIDIdx >= len(row) || nameIdx >= len(row) {
					continue
				}
				clinicNameCache.data[strings.TrimSpace(row[clinicIDIdx])] = strings.TrimSpace(row[nameIdx])
			}
			return
		}
	})
	return clinicNameCache.data[strings.TrimSpace(clinicID)]
}

func writeRawJSONOrFallback(w http.ResponseWriter, statusCode int, responseBody []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if len(responseBody) == 0 {
		_, _ = w.Write([]byte(`{"message":"upstream request failed"}`))
		return
	}
	_, _ = w.Write(responseBody)
}
