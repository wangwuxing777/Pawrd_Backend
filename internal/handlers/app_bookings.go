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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/merchant"
	"gorm.io/gorm"
)

type appBookingRequest struct {
	ClinicID        string    `json:"clinic_id"`
	BookingClinicID string    `json:"booking_clinic_id"`
	ClinicName      string    `json:"clinic_name"`
	PetID           string    `json:"pet_id"`
	ServiceType     string    `json:"service_type"`
	ScheduledDate   time.Time `json:"scheduled_date"`
	Notes           string    `json:"notes"`
}

type appBookingPatchRequest struct {
	Status string `json:"status"`
	Notes  string `json:"notes"`
}

type appBookingDTO struct {
	ID                string              `json:"id"`
	ClinicID          string              `json:"clinic_id"`
	ClinicName        string              `json:"clinic_name"`
	ServiceType       string              `json:"service_type"`
	ScheduledDate     time.Time           `json:"scheduled_date"`
	Status            string              `json:"status"`
	SyncState         string              `json:"sync_state"`
	IsStale           bool                `json:"is_stale"`
	LastSyncedAt      *time.Time          `json:"last_synced_at,omitempty"`
	MerchantUpdatedAt *time.Time          `json:"merchant_updated_at,omitempty"`
	LastSyncSource    string              `json:"last_sync_source,omitempty"`
	Notes             string              `json:"notes,omitempty"`
	PetID             string              `json:"pet_id"`
	PetName           string              `json:"pet_name"`
	CreatedAt         time.Time           `json:"created_at"`
	Debug             *appBookingDebugDTO `json:"debug,omitempty"`
}

type appBookingDebugDTO struct {
	BookingClinicID               string     `json:"booking_clinic_id,omitempty"`
	MerchantInternalAppointmentID *uint      `json:"merchant_internal_appointment_id,omitempty"`
	RequestID                     string     `json:"request_id,omitempty"`
	IdempotencyKey                string     `json:"idempotency_key,omitempty"`
	LastSyncAttemptAt             *time.Time `json:"last_sync_attempt_at,omitempty"`
	LastSyncError                 string     `json:"last_sync_error,omitempty"`
}

type appBookingReconcileResultDTO struct {
	ExternalBookingID string `json:"external_booking_id"`
	Action            string `json:"action"`
	SyncState         string `json:"sync_state"`
	RequestID         string `json:"request_id,omitempty"`
	LastSyncSource    string `json:"last_sync_source,omitempty"`
	LastSyncError     string `json:"last_sync_error,omitempty"`
}

type appBookingReconcileOptions struct {
	Limit                   int
	TargetExternalBookingID string
	SyncStateFilter         string
	DryRun                  bool
	Force                   bool
	IncludeDebug            bool
	RequestID               string
	SyncSource              string
}

type appBookingReconcileSummary struct {
	Limit                       int                            `json:"limit"`
	DryRun                      bool                           `json:"dry_run"`
	Force                       bool                           `json:"force"`
	SyncState                   string                         `json:"sync_state"`
	ExternalBookingID           string                         `json:"external_booking_id"`
	ScannedCount                int                            `json:"scanned_count"`
	EligibleCount               int                            `json:"eligible_count"`
	RefreshedCount              int                            `json:"refreshed_count"`
	CountsBySyncState           map[string]int                 `json:"counts_by_sync_state"`
	EligibleExternalBookingIDs  []string                       `json:"eligible_external_booking_ids"`
	RefreshedExternalBookingIDs []string                       `json:"refreshed_external_booking_ids"`
	SkippedExternalBookingIDs   []string                       `json:"skipped_external_booking_ids"`
	Results                     []appBookingReconcileResultDTO `json:"results,omitempty"`
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

type appBookingSyncRequest struct {
	ExternalBookingID   string `json:"external_booking_id"`
	ClinicIntegrationID string `json:"clinic_integration_id"`
	ClinicName          string `json:"clinic_name"`
	ScheduledAt         string `json:"scheduled_at"`
	Status              string `json:"status"`
	UpdatedAt           string `json:"updated_at"`
	MerchantStatus      struct {
		AppointmentStatus     string `json:"appointment_status"`
		InternalAppointmentID uint   `json:"internal_appointment_id"`
	} `json:"merchant_status"`
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
			AppointmentStatus     string `json:"appointment_status"`
			InternalAppointmentID uint   `json:"internal_appointment_id"`
		} `json:"merchant_status"`
	} `json:"data"`
}

var clinicNameCache = struct {
	once sync.Once
	data map[string]string
}{}

var mirrorFreshnessWindow = 2 * time.Minute

func SetMirrorFreshnessWindow(window time.Duration) {
	if window > 0 {
		mirrorFreshnessWindow = window
	}
}

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

func NewAppBookingSyncHandler(db *gorm.DB, sharedSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.TrimSpace(sharedSecret) == "" {
			http.Error(w, "Booking sync handler is not configured", http.StatusServiceUnavailable)
			return
		}
		if strings.TrimSpace(r.Header.Get("X-Booking-Sync-Token")) != strings.TrimSpace(sharedSecret) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		var req appBookingSyncRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		req.ExternalBookingID = strings.TrimSpace(req.ExternalBookingID)
		req.ClinicIntegrationID = strings.TrimSpace(req.ClinicIntegrationID)
		req.ClinicName = strings.TrimSpace(req.ClinicName)
		req.ScheduledAt = strings.TrimSpace(req.ScheduledAt)
		req.Status = strings.TrimSpace(req.Status)
		req.MerchantStatus.AppointmentStatus = strings.TrimSpace(req.MerchantStatus.AppointmentStatus)
		req.UpdatedAt = strings.TrimSpace(req.UpdatedAt)

		if req.ExternalBookingID == "" {
			http.Error(w, "external_booking_id is required", http.StatusBadRequest)
			return
		}

		var mirror models.AppBookingMirror
		if err := db.Where("external_booking_id = ?", req.ExternalBookingID).First(&mirror).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				http.Error(w, "Booking not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Failed to fetch booking", http.StatusInternalServerError)
			return
		}

		now := time.Now().UTC()
		requestID := resolveRequestID(r)
		mirror.LastSyncAttemptAt = &now
		mirror.LastSyncedAt = &now
		mirror.LastSyncError = ""
		mirror.LastSyncSource = "merchant_sync"
		mirror.RequestID = requestID
		if req.Status != "" {
			mirror.Status = mapMerchantStatusToAppStatus(req.Status)
		}
		if req.MerchantStatus.AppointmentStatus != "" {
			mirror.MerchantStatus = req.MerchantStatus.AppointmentStatus
		}
		if req.MerchantStatus.InternalAppointmentID != 0 {
			id := req.MerchantStatus.InternalAppointmentID
			mirror.MerchantInternalAppointmentID = &id
		}
		if req.ClinicIntegrationID != "" {
			mirror.BookingClinicID = req.ClinicIntegrationID
		}
		if req.ClinicName != "" {
			mirror.ClinicName = req.ClinicName
		}
		if scheduledAt := parseMerchantTimestamp(req.ScheduledAt); scheduledAt != nil {
			mirror.ScheduledDate = *scheduledAt
		}
		if merchantUpdatedAt := parseMerchantTimestamp(req.UpdatedAt); merchantUpdatedAt != nil {
			mirror.MerchantUpdatedAt = merchantUpdatedAt
		}

		if err := db.Save(&mirror).Error; err != nil {
			http.Error(w, "Failed to update booking mirror", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", requestID)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data":    bookingDTOFromMirror(mirror),
		})
	}
}

func NewAppBookingReconcileHandler(db *gorm.DB, gateway VaccinationFacadeGateway, sharedSecret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.TrimSpace(sharedSecret) == "" {
			http.Error(w, "Booking reconcile handler is not configured", http.StatusServiceUnavailable)
			return
		}
		if strings.TrimSpace(r.Header.Get("X-Booking-Sync-Token")) != strings.TrimSpace(sharedSecret) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		limit := 20
		targetExternalBookingID := strings.TrimSpace(r.URL.Query().Get("external_booking_id"))
		clinicIDFilter := strings.TrimSpace(r.URL.Query().Get("clinic_id"))
		petIDFilter := strings.TrimSpace(r.URL.Query().Get("pet_id"))
		requestIDFilter := strings.TrimSpace(r.URL.Query().Get("request_id"))
		syncStateFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sync_state")))
		if syncStateFilter != "" && syncStateFilter != "stale" && syncStateFilter != "never_synced" && syncStateFilter != "sync_error" {
			http.Error(w, "invalid sync_state", http.StatusBadRequest)
			return
		}
		if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
			if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 {
				if parsed > 100 {
					parsed = 100
				}
				limit = parsed
			}
		}

		var mirrors []models.AppBookingMirror
		query := db.Order("updated_at ASC").Limit(limit)
		if targetExternalBookingID != "" {
			query = query.Where("external_booking_id = ?", targetExternalBookingID)
		}
		if clinicIDFilter != "" {
			query = query.Where("clinic_id = ?", clinicIDFilter)
		}
		if petIDFilter != "" {
			query = query.Where("pet_id = ?", petIDFilter)
		}
		if requestIDFilter != "" {
			query = query.Where("request_id = ?", requestIDFilter)
		}
		if err := query.Find(&mirrors).Error; err != nil {
			http.Error(w, "Failed to fetch bookings", http.StatusInternalServerError)
			return
		}
		if targetExternalBookingID != "" && len(mirrors) == 0 {
			http.Error(w, "Booking not found", http.StatusNotFound)
			return
		}

		requestID := resolveRequestID(r)
		dryRun := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("dry_run")), "true")
		force := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("force")), "true")
		includeDebug := requestIncludesDebug(r)
		eligibleCount := 0
		refreshedCount := 0
		countsBySyncState := map[string]int{
			"fresh":        0,
			"stale":        0,
			"never_synced": 0,
			"sync_error":   0,
		}
		eligibleExternalBookingIDs := make([]string, 0)
		refreshedExternalBookingIDs := make([]string, 0)
		skippedExternalBookingIDs := make([]string, 0)
		results := make([]appBookingReconcileResultDTO, 0)
		for i := range mirrors {
			currentSyncState, _ := deriveMirrorSyncState(mirrors[i], time.Now().UTC())
			countsBySyncState[currentSyncState]++
			if syncStateFilter != "" {
				if currentSyncState != syncStateFilter {
					skippedExternalBookingIDs = append(skippedExternalBookingIDs, mirrors[i].ExternalBookingID)
					if includeDebug {
						results = append(results, appBookingReconcileResultDTO{
							ExternalBookingID: mirrors[i].ExternalBookingID,
							Action:            "skipped",
							SyncState:         currentSyncState,
							RequestID:         mirrors[i].RequestID,
							LastSyncSource:    mirrors[i].LastSyncSource,
							LastSyncError:     mirrors[i].LastSyncError,
						})
					}
					continue
				}
			}
			if !force && !shouldRefreshMirror(mirrors[i], false) {
				skippedExternalBookingIDs = append(skippedExternalBookingIDs, mirrors[i].ExternalBookingID)
				if includeDebug {
					results = append(results, appBookingReconcileResultDTO{
						ExternalBookingID: mirrors[i].ExternalBookingID,
						Action:            "skipped",
						SyncState:         currentSyncState,
						RequestID:         mirrors[i].RequestID,
						LastSyncSource:    mirrors[i].LastSyncSource,
						LastSyncError:     mirrors[i].LastSyncError,
					})
				}
				continue
			}
			eligibleCount++
			eligibleExternalBookingIDs = append(eligibleExternalBookingIDs, mirrors[i].ExternalBookingID)
			if dryRun {
				if includeDebug {
					results = append(results, appBookingReconcileResultDTO{
						ExternalBookingID: mirrors[i].ExternalBookingID,
						Action:            "eligible_dry_run",
						SyncState:         currentSyncState,
						RequestID:         mirrors[i].RequestID,
						LastSyncSource:    mirrors[i].LastSyncSource,
						LastSyncError:     mirrors[i].LastSyncError,
					})
				}
				continue
			}
			refreshMirrorFromMerchant(r.Context(), db, gateway, &mirrors[i], requestID, "reconcile_refresh")
			refreshedCount++
			refreshedExternalBookingIDs = append(refreshedExternalBookingIDs, mirrors[i].ExternalBookingID)
			if includeDebug {
				nextSyncState, _ := deriveMirrorSyncState(mirrors[i], time.Now().UTC())
				results = append(results, appBookingReconcileResultDTO{
					ExternalBookingID: mirrors[i].ExternalBookingID,
					Action:            "refreshed",
					SyncState:         nextSyncState,
					RequestID:         mirrors[i].RequestID,
					LastSyncSource:    mirrors[i].LastSyncSource,
					LastSyncError:     mirrors[i].LastSyncError,
				})
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", requestID)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"limit":                          limit,
				"dry_run":                        dryRun,
				"force":                          force,
				"sync_state":                     syncStateFilter,
				"external_booking_id":            targetExternalBookingID,
				"clinic_id":                      clinicIDFilter,
				"pet_id":                         petIDFilter,
				"request_id":                     requestIDFilter,
				"scanned_count":                  len(mirrors),
				"counts_by_sync_state":           countsBySyncState,
				"eligible_count":                 eligibleCount,
				"refreshed_count":                refreshedCount,
				"eligible_external_booking_ids":  eligibleExternalBookingIDs,
				"refreshed_external_booking_ids": refreshedExternalBookingIDs,
				"skipped_external_booking_ids":   skippedExternalBookingIDs,
				"results": func() any {
					if includeDebug {
						return results
					}
					return nil
				}(),
			},
		})
	}
}

func handleCreateAppBooking(w http.ResponseWriter, r *http.Request, db *gorm.DB, gateway VaccinationFacadeGateway) {
	var req appBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	req.ClinicID = strings.TrimSpace(req.ClinicID)
	req.BookingClinicID = strings.TrimSpace(req.BookingClinicID)
	req.ClinicName = strings.TrimSpace(req.ClinicName)
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
		ClinicIntegrationID: resolveBookingClinicID(req),
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

	requestID := resolveRequestID(r)
	idempotencyKey := uuid.NewString()
	statusCode, _, responseBody, err := gateway.Send(r.Context(), http.MethodPost, "/app/v1/vaccinations/bookings", nil, payload, map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": idempotencyKey,
		"X-Request-ID":    requestID,
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
	merchantUpdatedAt := parseMerchantTimestamp(merchantResp.Data.UpdatedAt)
	lastSyncedAt := time.Now().UTC()

	mirror := models.AppBookingMirror{
		ExternalBookingID: merchantResp.Data.ExternalBookingID,
		ClinicID:          req.ClinicID,
		BookingClinicID:   merchantReq.ClinicIntegrationID,
		ClinicName:        resolveClinicName(req),
		ServiceType:       req.ServiceType,
		ScheduledDate:     req.ScheduledDate.UTC(),
		Status:            mapMerchantStatusToAppStatus(merchantResp.Data.Status),
		MerchantStatus:    merchantResp.Data.MerchantStatus.AppointmentStatus,
		MerchantInternalAppointmentID: func() *uint {
			if merchantResp.Data.MerchantStatus.InternalAppointmentID == 0 {
				return nil
			}
			id := merchantResp.Data.MerchantStatus.InternalAppointmentID
			return &id
		}(),
		MerchantUpdatedAt: merchantUpdatedAt,
		LastSyncAttemptAt: &lastSyncedAt,
		LastSyncedAt:      &lastSyncedAt,
		LastSyncError:     "",
		LastSyncSource:    "create_accept",
		RequestID:         requestID,
		IdempotencyKey:    idempotencyKey,
		Notes:             req.Notes,
		PetID:             req.PetID,
		PetName:           merchantReq.Pet.Name,
	}
	if err := db.Create(&mirror).Error; err != nil {
		http.Error(w, "Failed to persist booking mirror", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
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
	if externalBookingID := strings.TrimSpace(r.URL.Query().Get("external_booking_id")); externalBookingID != "" {
		query = query.Where("external_booking_id = ?", externalBookingID)
	}
	if clinicID := strings.TrimSpace(r.URL.Query().Get("clinic_id")); clinicID != "" {
		query = query.Where("clinic_id = ?", clinicID)
	}
	if bookingClinicID := strings.TrimSpace(r.URL.Query().Get("booking_clinic_id")); bookingClinicID != "" {
		query = query.Where("booking_clinic_id = ?", bookingClinicID)
	}
	if internalAppointmentID := strings.TrimSpace(r.URL.Query().Get("merchant_internal_appointment_id")); internalAppointmentID != "" {
		query = query.Where("merchant_internal_appointment_id = ?", internalAppointmentID)
	}
	if requestIDFilter := strings.TrimSpace(r.URL.Query().Get("request_id")); requestIDFilter != "" {
		query = query.Where("request_id = ?", requestIDFilter)
	}
	if since := strings.TrimSpace(r.URL.Query().Get("since")); since != "" {
		parsedSince, err := time.Parse(time.RFC3339, since)
		if err != nil {
			http.Error(w, "invalid since", http.StatusBadRequest)
			return
		}
		query = query.Where("updated_at >= ?", parsedSince.UTC())
	}
	if petID := strings.TrimSpace(r.URL.Query().Get("pet_id")); petID != "" {
		query = query.Where("pet_id = ?", petID)
	}
	lastSyncSourceFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("last_sync_source")))
	if lastSyncSourceFilter != "" {
		valid := map[string]bool{
			"create_accept":     true,
			"read_refresh":      true,
			"cancel_accept":     true,
			"merchant_sync":     true,
			"reconcile_refresh": true,
		}
		if !valid[lastSyncSourceFilter] {
			http.Error(w, "invalid last_sync_source", http.StatusBadRequest)
			return
		}
		query = query.Where("last_sync_source = ?", lastSyncSourceFilter)
	}
	if err := query.Find(&mirrors).Error; err != nil {
		http.Error(w, "Failed to fetch bookings", http.StatusInternalServerError)
		return
	}

	requestID := resolveRequestID(r)
	forceRefresh := requestForcesRefresh(r)
	syncStateFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sync_state")))
	if syncStateFilter != "" && syncStateFilter != "fresh" && syncStateFilter != "stale" && syncStateFilter != "never_synced" && syncStateFilter != "sync_error" {
		http.Error(w, "invalid sync_state", http.StatusBadRequest)
		return
	}
	for i := range mirrors {
		if syncStateFilter != "" && !forceRefresh {
			continue
		}
		if shouldRefreshMirror(mirrors[i], forceRefresh) {
			refreshMirrorFromMerchant(r.Context(), db, gateway, &mirrors[i], requestID, "read_refresh")
		}
	}

	includeDebug := requestIncludesDebug(r)
	bookings := make([]appBookingDTO, 0, len(mirrors))
	for _, mirror := range mirrors {
		dto := bookingDTOFromMirrorWithDebug(mirror, includeDebug)
		if syncStateFilter != "" && dto.SyncState != syncStateFilter {
			continue
		}
		bookings = append(bookings, dto)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
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
	requestID := resolveRequestID(r)
	syncStateFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("sync_state")))
	if syncStateFilter != "" && syncStateFilter != "fresh" && syncStateFilter != "stale" && syncStateFilter != "never_synced" && syncStateFilter != "sync_error" {
		http.Error(w, "invalid sync_state", http.StatusBadRequest)
		return
	}
	if shouldRefreshMirror(mirror, requestForcesRefresh(r)) {
		refreshMirrorFromMerchant(r.Context(), db, gateway, &mirror, requestID, "read_refresh")
	}
	dto := bookingDTOFromMirrorWithDebug(mirror, requestIncludesDebug(r))
	if syncStateFilter != "" && dto.SyncState != syncStateFilter {
		http.Error(w, "booking sync_state does not match requested filter", http.StatusConflict)
		return
	}
	if expectedExternalBookingID := strings.TrimSpace(r.URL.Query().Get("external_booking_id")); expectedExternalBookingID != "" && mirror.ExternalBookingID != expectedExternalBookingID {
		http.Error(w, "booking external_booking_id does not match requested filter", http.StatusConflict)
		return
	}
	if expectedRequestID := strings.TrimSpace(r.URL.Query().Get("request_id")); expectedRequestID != "" && mirror.RequestID != expectedRequestID {
		http.Error(w, "booking request_id does not match requested filter", http.StatusConflict)
		return
	}
	if expectedBookingClinicID := strings.TrimSpace(r.URL.Query().Get("booking_clinic_id")); expectedBookingClinicID != "" && mirror.BookingClinicID != expectedBookingClinicID {
		http.Error(w, "booking booking_clinic_id does not match requested filter", http.StatusConflict)
		return
	}
	if expectedInternalAppointmentID := strings.TrimSpace(r.URL.Query().Get("merchant_internal_appointment_id")); expectedInternalAppointmentID != "" {
		currentInternalAppointmentID := ""
		if mirror.MerchantInternalAppointmentID != nil {
			currentInternalAppointmentID = strconv.FormatUint(uint64(*mirror.MerchantInternalAppointmentID), 10)
		}
		if currentInternalAppointmentID != expectedInternalAppointmentID {
			http.Error(w, "booking merchant_internal_appointment_id does not match requested filter", http.StatusConflict)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", requestID)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    dto,
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
		lastSyncAttemptAt := time.Now().UTC()
		requestID := resolveRequestID(r)
		mirror.LastSyncAttemptAt = &lastSyncAttemptAt
		mirror.RequestID = requestID
		payload, _ := json.Marshal(map[string]string{"reason": strings.TrimSpace(req.Notes)})
		statusCode, _, responseBody, err := gateway.Send(r.Context(), http.MethodPost, "/app/v1/vaccinations/bookings/"+url.PathEscape(mirror.ExternalBookingID)+"/cancel", nil, payload, map[string]string{
			"Content-Type": "application/json",
			"X-Request-ID": requestID,
		})
		if err != nil {
			mirror.LastSyncError = "gateway_error"
			_ = db.Save(&mirror).Error
			if errors.Is(err, merchant.ErrNotConfigured) {
				http.Error(w, "Merchant vaccination gateway is not configured", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "Failed to contact merchant vaccination gateway", http.StatusBadGateway)
			return
		}
		if statusCode < 200 || statusCode > 299 {
			mirror.LastSyncError = "upstream_status_" + http.StatusText(statusCode)
			_ = db.Save(&mirror).Error
			writeRawJSONOrFallback(w, statusCode, responseBody)
			return
		}
		lastSyncedAt := time.Now().UTC()
		mirror.LastSyncedAt = &lastSyncedAt
		mirror.LastSyncError = ""
		mirror.LastSyncSource = "cancel_accept"

		var merchantResp merchantBookingEnvelope
		if err := json.Unmarshal(responseBody, &merchantResp); err == nil {
			if nextStatus := mapMerchantStatusToAppStatus(merchantResp.Data.Status); nextStatus != "" {
				mirror.Status = nextStatus
			} else {
				mirror.Status = "cancelled"
			}
			if merchantResp.Data.MerchantStatus.AppointmentStatus != "" {
				mirror.MerchantStatus = merchantResp.Data.MerchantStatus.AppointmentStatus
			} else {
				mirror.MerchantStatus = "cancelled"
			}
			if merchantUpdatedAt := parseMerchantTimestamp(merchantResp.Data.UpdatedAt); merchantUpdatedAt != nil {
				mirror.MerchantUpdatedAt = merchantUpdatedAt
			}
		} else {
			mirror.Status = "cancelled"
			mirror.MerchantStatus = "cancelled"
		}
	}

	if desiredStatus != "cancelled" && strings.TrimSpace(req.Notes) != "" {
		mirror.Notes = strings.TrimSpace(req.Notes)
	}
	if err := db.Save(&mirror).Error; err != nil {
		http.Error(w, "Failed to update booking mirror", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if mirror.RequestID != "" {
		w.Header().Set("X-Request-ID", mirror.RequestID)
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data": map[string]interface{}{
			"id":         mirror.ID,
			"status":     mirror.Status,
			"updated_at": mirror.UpdatedAt,
		},
	})
}

func refreshMirrorFromMerchant(ctx context.Context, db *gorm.DB, gateway VaccinationFacadeGateway, mirror *models.AppBookingMirror, requestID string, syncSource string) {
	if mirror.ExternalBookingID == "" {
		return
	}
	lastSyncAttemptAt := time.Now().UTC()
	mirror.LastSyncAttemptAt = &lastSyncAttemptAt
	if requestID != "" {
		mirror.RequestID = requestID
	}
	headers := map[string]string{}
	if requestID != "" {
		headers["X-Request-ID"] = requestID
	}
	statusCode, _, responseBody, err := gateway.Send(ctx, http.MethodGet, "/app/v1/vaccinations/bookings/"+url.PathEscape(mirror.ExternalBookingID), nil, nil, headers)
	if err != nil {
		mirror.LastSyncError = "gateway_error"
		_ = db.Save(mirror).Error
		return
	}
	if statusCode < 200 || statusCode > 299 {
		mirror.LastSyncError = "upstream_status_" + http.StatusText(statusCode)
		_ = db.Save(mirror).Error
		return
	}

	var merchantResp merchantBookingEnvelope
	if err := json.Unmarshal(responseBody, &merchantResp); err != nil {
		mirror.LastSyncError = "invalid_response"
		_ = db.Save(mirror).Error
		return
	}

	updated := false
	lastSyncedAt := time.Now().UTC()
	if mirror.LastSyncedAt == nil || !mirror.LastSyncedAt.Equal(lastSyncedAt) {
		mirror.LastSyncedAt = &lastSyncedAt
		updated = true
	}
	if mirror.LastSyncError != "" {
		mirror.LastSyncError = ""
		updated = true
	}
	if syncSource == "" {
		syncSource = "read_refresh"
	}
	if mirror.LastSyncSource != syncSource {
		mirror.LastSyncSource = syncSource
		updated = true
	}
	if merchantUpdatedAt := parseMerchantTimestamp(merchantResp.Data.UpdatedAt); merchantUpdatedAt != nil {
		if mirror.MerchantUpdatedAt == nil || !mirror.MerchantUpdatedAt.Equal(*merchantUpdatedAt) {
			mirror.MerchantUpdatedAt = merchantUpdatedAt
			updated = true
		}
	}
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
	if merchantResp.Data.MerchantStatus.InternalAppointmentID != 0 {
		if mirror.MerchantInternalAppointmentID == nil || *mirror.MerchantInternalAppointmentID != merchantResp.Data.MerchantStatus.InternalAppointmentID {
			id := merchantResp.Data.MerchantStatus.InternalAppointmentID
			mirror.MerchantInternalAppointmentID = &id
			updated = true
		}
	}
	if updated {
		_ = db.Save(mirror).Error
	}
}

func parseMerchantTimestamp(raw string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	utc := parsed.UTC()
	return &utc
}

func resolveRequestID(r *http.Request) string {
	for _, key := range []string{"X-Request-ID", "X-Correlation-ID"} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return value
		}
	}
	return uuid.NewString()
}

func bookingDTOFromMirror(mirror models.AppBookingMirror) appBookingDTO {
	return bookingDTOFromMirrorAtWithDebug(mirror, time.Now().UTC(), false)
}

func bookingDTOFromMirrorWithDebug(mirror models.AppBookingMirror, includeDebug bool) appBookingDTO {
	return bookingDTOFromMirrorAtWithDebug(mirror, time.Now().UTC(), includeDebug)
}

func bookingDTOFromMirrorAt(mirror models.AppBookingMirror, now time.Time) appBookingDTO {
	return bookingDTOFromMirrorAtWithDebug(mirror, now, false)
}

func bookingDTOFromMirrorAtWithDebug(mirror models.AppBookingMirror, now time.Time, includeDebug bool) appBookingDTO {
	syncState, isStale := deriveMirrorSyncState(mirror, now)
	dto := appBookingDTO{
		ID:                mirror.ID,
		ClinicID:          mirror.ClinicID,
		ClinicName:        mirror.ClinicName,
		ServiceType:       mirror.ServiceType,
		ScheduledDate:     mirror.ScheduledDate,
		Status:            mirror.Status,
		SyncState:         syncState,
		IsStale:           isStale,
		LastSyncedAt:      mirror.LastSyncedAt,
		MerchantUpdatedAt: mirror.MerchantUpdatedAt,
		LastSyncSource:    mirror.LastSyncSource,
		Notes:             mirror.Notes,
		PetID:             mirror.PetID,
		PetName:           mirror.PetName,
		CreatedAt:         mirror.CreatedAt,
	}
	if includeDebug {
		dto.Debug = &appBookingDebugDTO{
			BookingClinicID:               mirror.BookingClinicID,
			MerchantInternalAppointmentID: mirror.MerchantInternalAppointmentID,
			RequestID:                     mirror.RequestID,
			IdempotencyKey:                mirror.IdempotencyKey,
			LastSyncAttemptAt:             mirror.LastSyncAttemptAt,
			LastSyncError:                 mirror.LastSyncError,
		}
	}
	return dto
}

func requestIncludesDebug(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_debug")), "true")
}

func deriveMirrorSyncState(mirror models.AppBookingMirror, now time.Time) (string, bool) {
	if mirror.LastSyncError != "" {
		return "sync_error", true
	}
	if mirror.LastSyncedAt == nil {
		return "never_synced", true
	}
	if now.Sub(mirror.LastSyncedAt.UTC()) > mirrorFreshnessWindow {
		return "stale", true
	}
	return "fresh", false
}

func shouldRefreshMirror(mirror models.AppBookingMirror, forceRefresh bool) bool {
	if forceRefresh {
		return true
	}
	syncState, _ := deriveMirrorSyncState(mirror, time.Now().UTC())
	return syncState != "fresh"
}

func requestForcesRefresh(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("force_refresh")), "true")
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

func resolveBookingClinicID(req appBookingRequest) string {
	if req.BookingClinicID != "" {
		return req.BookingClinicID
	}
	if mappedBookingClinicID := bookingClinicIDForPublicClinicID(req.ClinicID); mappedBookingClinicID != "" {
		return mappedBookingClinicID
	}
	return req.ClinicID
}

func resolveClinicName(req appBookingRequest) string {
	if req.ClinicName != "" {
		return req.ClinicName
	}
	if clinicName := lookupClinicName(req.ClinicID); clinicName != "" {
		return clinicName
	}
	return req.ClinicID
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
