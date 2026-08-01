package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/gorm"
)

func NewShippingAddressesHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPut {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID, ok := authenticatedUserID(w, r)
		if !ok {
			return
		}

		switch r.Method {
		case http.MethodGet:
			var addresses []models.ShippingAddress
			if err := db.Where("user_id = ?", userID).
				Order("sort_order ASC, created_at ASC").
				Find(&addresses).Error; err != nil {
				http.Error(w, "failed to load shipping addresses", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"addresses": addresses})

		case http.MethodPut:
			var req struct {
				Addresses []models.ShippingAddress `json:"addresses"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeAuthError(w, http.StatusBadRequest, "Invalid request body")
				return
			}

			seen := map[string]bool{}
			for index := range req.Addresses {
				address := &req.Addresses[index]
				address.ID = strings.TrimSpace(address.ID)
				address.UserID = userID
				address.Label = strings.TrimSpace(address.Label)
				address.RecipientName = strings.TrimSpace(address.RecipientName)
				address.Address = strings.TrimSpace(address.Address)
				address.RegionID = strings.TrimSpace(address.RegionID)
				address.DistrictID = strings.TrimSpace(address.DistrictID)
				address.SortOrder = index

				normalizedPhone, err := normalizeHongKongPhone(address.Phone)
				if err != nil || address.RecipientName == "" ||
					address.Address == "" || address.RegionID == "" ||
					address.DistrictID == "" {
					writeAuthError(w, http.StatusBadRequest, "Shipping address is incomplete or phone is invalid")
					return
				}
				address.Phone = normalizedPhone

				if address.ID == "" {
					address.ID = uuid.NewString()
				}
				if seen[address.ID] {
					writeAuthError(w, http.StatusBadRequest, "Shipping address IDs must be unique")
					return
				}
				seen[address.ID] = true
			}

			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("user_id = ?", userID).
					Delete(&models.ShippingAddress{}).Error; err != nil {
					return err
				}
				if len(req.Addresses) > 0 {
					if err := tx.Create(&req.Addresses).Error; err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				http.Error(w, "failed to replace shipping addresses", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"addresses": req.Addresses})
		}
	}
}
