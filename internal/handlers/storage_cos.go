package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/objectstore"
)

func NewCOSPresignUploadHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			PetID    string `json:"pet_id"`
			Filename string `json:"filename"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		req.PetID = strings.TrimSpace(req.PetID)
		req.Filename = strings.TrimSpace(req.Filename)
		if req.PetID == "" || req.Filename == "" {
			http.Error(w, "pet_id and filename are required", http.StatusBadRequest)
			return
		}

		store, err := objectstore.NewCOSStoreFromEnv()
		if err != nil {
			http.Error(w, "cos config error: "+err.Error(), http.StatusBadRequest)
			return
		}
		objectKey := store.BuildObjectKey(req.PetID, req.Filename)
		uploadURL, expiresIn, err := store.PresignUpload(objectKey)
		if err != nil {
			http.Error(w, "failed to generate upload url: "+err.Error(), http.StatusInternalServerError)
			return
		}
		readURL, _, err := store.PresignRead(objectKey)
		if err != nil {
			http.Error(w, "failed to generate read url: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"object_key":         objectKey,
			"upload_url":         uploadURL,
			"read_url":           readURL,
			"expires_in_seconds": int(expiresIn.Seconds()),
		})
	}
}
