package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/auth"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/gorm"
)

// ── Middleware: extract user ID from JWT ────────────────────────────────────

func authMiddleware(next func(w http.ResponseWriter, userID uint, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}

		header := r.Header.Get("Authorization")
		if header == "" {
			writeJSONError(w, http.StatusUnauthorized, "Missing authorization header")
			return
		}

		tokenStr := strings.TrimPrefix(header, "Bearer ")
		claims, err := auth.ValidateToken(tokenStr)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "Invalid or expired token")
			return
		}

		userID := parseUint(claims.UserID)
		next(w, userID, r)
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ── User Search: GET /api/users/search?q=... ────────────────────────────────

type SearchedUserResponse struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	AvatarURL   string `json:"avatarUrl"`
	FriendStatus string `json:"friendStatus"`
}

func NewUserSearchHandler(db *gorm.DB) http.HandlerFunc {
	return authMiddleware(func(w http.ResponseWriter, userID uint, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET is allowed")
			return
		}

		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(q) < 2 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]SearchedUserResponse{})
			return
		}

		// Search users by username, name, or email
		var users []models.AuthUser
		db.Where("(username LIKE ? OR name LIKE ? OR email LIKE ?) AND id != ?",
			"%"+q+"%", "%"+q+"%", "%"+q+"%", userID).Find(&users)

		// Build friend status map
		friendIDs := map[uint]string{}
		var outgoingIDs []uint
		var incomingIDs []uint

		// Friends
		var friendships []models.Friend
		db.Where("user_id = ?", userID).Find(&friendships)
		for _, f := range friendships {
			friendIDs[f.FriendUserID] = "friends"
		}

		// Pending outgoing (I sent request)
		var outgoing []models.FriendRequest
		db.Where("sender_id = ? AND status = ?", userID, "pending").Find(&outgoing)
		for _, o := range outgoing {
			outgoingIDs = append(outgoingIDs, o.ReceiverID)
		}

		// Pending incoming (someone sent me request)
		var incoming []models.FriendRequest
		db.Where("receiver_id = ? AND status = ?", userID, "pending").Find(&incoming)
		for _, i := range incoming {
			incomingIDs = append(incomingIDs, i.SenderID)
		}

		results := make([]SearchedUserResponse, 0, len(users))
		for _, u := range users {
			status := "none"
			if _, ok := friendIDs[u.ID]; ok {
				status = "friends"
			} else if containsUint(outgoingIDs, u.ID) {
				status = "pendingOutgoing"
			} else if containsUint(incomingIDs, u.ID) {
				status = "pendingIncoming"
			}

			results = append(results, SearchedUserResponse{
				ID:           fmt.Sprintf("%d", u.ID),
				Username:     u.Username,
				Name:         u.Name,
				Email:        u.Email,
				AvatarURL:    u.AvatarURL,
				FriendStatus: status,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	})
}

// ── Get Friends: GET /api/friends ───────────────────────────────────────────

type FriendsListResponse struct {
	Friends       []FriendItemResponse    `json:"friends"`
	PendingRequests []FriendRequestResponse `json:"pending_requests"`
}

type FriendItemResponse struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	Name           string `json:"name"`
	Email          string `json:"email"`
	AvatarURL      string `json:"avatarUrl,omitempty"`
	FriendshipDate string `json:"friendshipDate"`
}

type FriendRequestResponse struct {
	ID         string `json:"id"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	SenderAvatar string `json:"sender_avatar,omitempty"`
	RequestedAt string `json:"requested_at"`
}

func NewFriendsListHandler(db *gorm.DB) http.HandlerFunc {
	return authMiddleware(func(w http.ResponseWriter, userID uint, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only GET is allowed")
			return
		}

		// Friends
		var friendships []models.Friend
		db.Where("user_id = ?", userID).Find(&friendships)

		var friendUserIDs []uint
		for _, f := range friendships {
			friendUserIDs = append(friendUserIDs, f.FriendUserID)
		}

		var friendUsers []models.AuthUser
		if len(friendUserIDs) > 0 {
			db.Where("id IN ?", friendUserIDs).Find(&friendUsers)
		}

		friendItems := make([]FriendItemResponse, 0, len(friendUsers))
		for _, u := range friendUsers {
			var f models.Friend
			db.Where("user_id = ? AND friend_user_id = ?", userID, u.ID).First(&f)
			friendItems = append(friendItems, FriendItemResponse{
				ID:             fmt.Sprintf("%d", f.ID),
				UserID:         fmt.Sprintf("%d", u.ID),
				Name:           u.Name,
				Email:          u.Email,
				AvatarURL:      u.AvatarURL,
				FriendshipDate: f.FriendshipDate.Format(time.RFC3339),
			})
		}

		// Pending incoming requests
		var requests []models.FriendRequest
		db.Where("receiver_id = ? AND status = ?", userID, "pending").Find(&requests)

		var senderIDs []uint
		for _, req := range requests {
			senderIDs = append(senderIDs, req.SenderID)
		}

		var senders []models.AuthUser
		if len(senderIDs) > 0 {
			db.Where("id IN ?", senderIDs).Find(&senders)
		}

		senderMap := map[uint]models.AuthUser{}
		for _, s := range senders {
			senderMap[s.ID] = s
		}

		pendingItems := make([]FriendRequestResponse, 0, len(requests))
		for _, req := range requests {
			sender, ok := senderMap[req.SenderID]
			if !ok {
				continue
			}
			pendingItems = append(pendingItems, FriendRequestResponse{
				ID:           fmt.Sprintf("%d", req.ID),
				SenderID:     fmt.Sprintf("%d", req.SenderID),
				SenderName:   sender.Name,
				SenderAvatar: sender.AvatarURL,
				RequestedAt:  req.CreatedAt.Format(time.RFC3339),
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(FriendsListResponse{
			Friends:       friendItems,
			PendingRequests: pendingItems,
		})
	})
}

// ── Send Friend Request: POST /api/friends/request ──────────────────────────

type SendFriendRequest struct {
	UserID string `json:"user_id"`
}

func NewSendFriendRequestHandler(db *gorm.DB) http.HandlerFunc {
	return authMiddleware(func(w http.ResponseWriter, userID uint, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST is allowed")
			return
		}

		var req SendFriendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		targetID := parseUint(req.UserID)
		if targetID == 0 || targetID == userID {
			writeJSONError(w, http.StatusBadRequest, "Invalid user ID")
			return
		}

		// Check target exists
		var target models.AuthUser
		if err := db.First(&target, targetID).Error; err != nil {
			writeJSONError(w, http.StatusNotFound, "User not found")
			return
		}

		// Check already friends
		var existingFriend models.Friend
		if err := db.Where("(user_id = ? AND friend_user_id = ?) OR (user_id = ? AND friend_user_id = ?)",
			userID, targetID, targetID, userID).First(&existingFriend).Error; err == nil {
			writeJSONError(w, http.StatusConflict, "Already friends")
			return
		}

		// Check pending request already exists
		var existingReq models.FriendRequest
		if err := db.Where("(sender_id = ? AND receiver_id = ? AND status = ?) OR (sender_id = ? AND receiver_id = ? AND status = ?)",
			userID, targetID, "pending", targetID, userID, "pending").First(&existingReq).Error; err == nil {
			writeJSONError(w, http.StatusConflict, "Friend request already pending")
			return
		}

		friendReq := models.FriendRequest{
			SenderID:   userID,
			ReceiverID: targetID,
			Status:     "pending",
		}
		if err := db.Create(&friendReq).Error; err != nil {
			writeJSONError(w, http.StatusInternalServerError, "Failed to send friend request")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"message": "Friend request sent"})
	})
}

// ── Respond to Friend Request: POST /api/friends/respond ────────────────────

type RespondRequest struct {
	RequestID string `json:"request_id"`
	Action    string `json:"action"` // accept or reject
}

func NewRespondFriendRequestHandler(db *gorm.DB) http.HandlerFunc {
	return authMiddleware(func(w http.ResponseWriter, userID uint, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "Only POST is allowed")
			return
		}

		var req RespondRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		reqID := parseUint(req.RequestID)
		if reqID == 0 {
			writeJSONError(w, http.StatusBadRequest, "Invalid request ID")
			return
		}

		if req.Action != "accept" && req.Action != "reject" {
			writeJSONError(w, http.StatusBadRequest, "Action must be 'accept' or 'reject'")
			return
		}

		var friendReq models.FriendRequest
		if err := db.Where("id = ? AND receiver_id = ? AND status = ?", reqID, userID, "pending").First(&friendReq).Error; err != nil {
			writeJSONError(w, http.StatusNotFound, "Friend request not found")
			return
		}

		if req.Action == "accept" {
			friendReq.Status = "accepted"
			db.Save(&friendReq)

			// Create bidirectional friend records
			now := time.Now()
			db.Create(&models.Friend{UserID: userID, FriendUserID: friendReq.SenderID, FriendshipDate: now})
			db.Create(&models.Friend{UserID: friendReq.SenderID, FriendUserID: userID, FriendshipDate: now})
		} else {
			friendReq.Status = "rejected"
			db.Save(&friendReq)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Friend request " + req.Action + "ed"})
	})
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func parseUint(s string) uint {
	var n uint
	fmt.Sscanf(s, "%d", &n)
	return n
}

func containsUint(ids []uint, target uint) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
