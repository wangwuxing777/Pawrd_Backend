package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/gorm"
)

// NewUserFollowHandler returns the handler for POST /users/{id}/follow.
// It toggles the follow state for the requesting user (identified by X-User-Id).
// Response: { "following": bool }.
func NewUserFollowHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		followeeID := strings.TrimSpace(r.PathValue("id"))
		if followeeID == "" {
			http.Error(w, "user id required", http.StatusBadRequest)
			return
		}

		followerID := strings.TrimSpace(r.Header.Get("X-User-Id"))
		if followerID == "" {
			http.Error(w, "missing X-User-Id", http.StatusUnauthorized)
			return
		}

		if followerID == followeeID {
			http.Error(w, "cannot follow yourself", http.StatusBadRequest)
			return
		}

		var following bool
		err := db.Transaction(func(tx *gorm.DB) error {
			var existing models.UserFollow
			err := tx.Where("follower_id = ? AND followee_id = ?", followerID, followeeID).First(&existing).Error
			switch {
			case err == nil:
				if delErr := tx.Delete(&existing).Error; delErr != nil {
					return delErr
				}
				following = false
			case errors.Is(err, gorm.ErrRecordNotFound):
				newFollow := models.UserFollow{FollowerID: followerID, FolloweeID: followeeID}
				if createErr := tx.Create(&newFollow).Error; createErr != nil {
					return createErr
				}
				following = true
			default:
				return err
			}
			return nil
		})

		if err != nil {
			http.Error(w, "failed to toggle follow: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"following": following,
		})
	}
}

// NewUserFollowersHandler returns GET /users/{id}/followers.
func NewUserFollowersHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := strings.TrimSpace(r.PathValue("id"))
		if userID == "" {
			http.Error(w, "user id required", http.StatusBadRequest)
			return
		}

		var followerIDs []string
		if err := db.Model(&models.UserFollow{}).
			Where("followee_id = ?", userID).
			Pluck("follower_id", &followerIDs).Error; err != nil {
			http.Error(w, "failed to fetch followers: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"users": followerIDs,
		})
	}
}

// NewUserFollowingHandler returns GET /users/{id}/following.
func NewUserFollowingHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := strings.TrimSpace(r.PathValue("id"))
		if userID == "" {
			http.Error(w, "user id required", http.StatusBadRequest)
			return
		}

		var followeeIDs []string
		if err := db.Model(&models.UserFollow{}).
			Where("follower_id = ?", userID).
			Pluck("followee_id", &followeeIDs).Error; err != nil {
			http.Error(w, "failed to fetch following: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"users": followeeIDs,
		})
	}
}

// NewUserStatsHandler returns GET /users/{id}/stats.
// Aggregates the headline counters shown on a profile:
//   - postCount:      posts authored by the user
//   - followerCount:  users following this user
//   - followingCount: users this user follows
//   - likeCount:      total likes received across all of the user's posts
//   - viewCount:      total views across all of the user's posts
//
// When called with an X-User-Id header, also reports whether the requester
// currently follows this user (`isFollowing`).
func NewUserStatsHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		userID := strings.TrimSpace(r.PathValue("id"))
		if userID == "" {
			http.Error(w, "user id required", http.StatusBadRequest)
			return
		}

		var postCount int64
		db.Model(&models.Post{}).Where("author_id = ?", userID).Count(&postCount)

		var followerCount int64
		db.Model(&models.UserFollow{}).Where("followee_id = ?", userID).Count(&followerCount)

		var followingCount int64
		db.Model(&models.UserFollow{}).Where("follower_id = ?", userID).Count(&followingCount)

		// Total likes received across the user's posts.
		var likeCount int64
		db.Model(&models.PostLike{}).
			Where("post_id IN (?)",
				db.Model(&models.Post{}).Select("id").Where("author_id = ?", userID),
			).Count(&likeCount)

		// Total views across the user's posts.
		var viewSum struct{ Total int64 }
		db.Model(&models.Post{}).
			Select("COALESCE(SUM(views), 0) AS total").
			Where("author_id = ?", userID).
			Scan(&viewSum)

		isFollowing := false
		if requester := strings.TrimSpace(r.Header.Get("X-User-Id")); requester != "" && requester != userID {
			var rel int64
			db.Model(&models.UserFollow{}).
				Where("follower_id = ? AND followee_id = ?", requester, userID).
				Count(&rel)
			isFollowing = rel > 0
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"postCount":      int(postCount),
			"followerCount":  int(followerCount),
			"followingCount": int(followingCount),
			"likeCount":      int(likeCount),
			"viewCount":      int(viewSum.Total),
			"isFollowing":    isFollowing,
		})
	}
}
