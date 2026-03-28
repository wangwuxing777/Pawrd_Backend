package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/gorm"
)

var partnerIDApproveRe = regexp.MustCompile(`^/api/admin/partners/(\d+)/approve$`)
var partnerIDRejectRe = regexp.MustCompile(`^/api/admin/partners/(\d+)/reject$`)

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Content-Type", "application/json")
}

// NewPartnersApplyHandler POST /api/partners/apply
func NewPartnersApplyHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			BusinessName string `json:"business_name"`
			ContactName  string `json:"contact_name"`
			ContactEmail string `json:"contact_email"`
			ContactPhone string `json:"contact_phone"`
			PartnerType  string `json:"partner_type"`
			District     string `json:"district"`
			Message      string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid json"})
			return
		}

		if strings.TrimSpace(req.BusinessName) == "" || strings.TrimSpace(req.ContactName) == "" ||
			strings.TrimSpace(req.ContactEmail) == "" || strings.TrimSpace(req.PartnerType) == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing required fields"})
			return
		}

		validTypes := map[string]bool{
			"clinic": true, "shop": true, "grooming": true, "boarding": true,
		}
		if !validTypes[req.PartnerType] {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid partner_type"})
			return
		}

		partner := models.Partner{
			BusinessName: req.BusinessName,
			ContactName:  req.ContactName,
			ContactEmail: req.ContactEmail,
			ContactPhone: req.ContactPhone,
			PartnerType:  models.PartnerType(req.PartnerType),
			District:     req.District,
			Message:      req.Message,
			Status:       models.PartnerStatusPending,
		}

		if err := db.Create(&partner).Error; err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "failed to save application"})
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "Application submitted successfully",
			"id":      partner.ID,
		})
	}
}

// NewPartnersAdminListHandler GET /api/admin/partners
func NewPartnersAdminListHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		statusFilter := r.URL.Query().Get("status")
		var partners []models.Partner
		q := db.Order("created_at DESC")
		if statusFilter != "" {
			q = q.Where("status = ?", statusFilter)
		}
		if err := q.Find(&partners).Error; err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"partners": partners,
			"total":    len(partners),
		})
	}
}

// NewPartnersAdminActionHandler PUT /api/admin/partners/{id}/approve or /reject
func NewPartnersAdminActionHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		path := r.URL.Path
		var idStr string
		var action string

		if m := partnerIDApproveRe.FindStringSubmatch(path); m != nil {
			idStr, action = m[1], "approve"
		} else if m := partnerIDRejectRe.FindStringSubmatch(path); m != nil {
			idStr, action = m[1], "reject"
		} else {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		id, err := strconv.Atoi(idStr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid id"})
			return
		}

		var partner models.Partner
		if err := db.First(&partner, id).Error; err != nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "partner not found"})
			return
		}

		var req struct {
			AdminNote string `json:"admin_note"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		updates := map[string]interface{}{"admin_note": req.AdminNote}

		if action == "approve" {
			apiKey, err := models.GenerateAPIKey()
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "failed to generate api key"})
				return
			}
			updates["status"] = models.PartnerStatusApproved
			updates["api_key"] = apiKey
		} else {
			updates["status"] = models.PartnerStatusRejected
		}

		if err := db.Model(&partner).Updates(updates).Error; err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		db.First(&partner, id) // reload
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "partner " + action + "d",
			"partner": partner,
		})
	}
}
