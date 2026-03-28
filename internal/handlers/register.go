package handlers

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
)

var (
	users      = make(map[string]models.User)
	usersMutex sync.RWMutex
)

func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	EnableCors(&w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var user models.User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	usersMutex.Lock()
	users[user.ID] = user
	usersMutex.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}
