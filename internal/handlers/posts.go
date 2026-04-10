package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/gorm"
)

// toBlogPost converts a fully-loaded models.Post (with Images/Likes/Comments preloaded)
// into the iOS-facing BlogPost shape, including the per-viewer `IsLiked` flag.
// `requesterID` may be empty for anonymous viewers (then `IsLiked` is always false).
func toBlogPost(p models.Post, requesterID string) models.BlogPost {
	imageUrls := make([]string, 0, len(p.Images))
	for _, img := range p.Images {
		imageUrls = append(imageUrls, img.ImageURL)
	}
	isLiked := false
	if requesterID != "" {
		for _, like := range p.Likes {
			if like.UserID == requesterID {
				isLiked = true
				break
			}
		}
	}
	return models.BlogPost{
		ID:           p.ID,
		AuthorID:     p.AuthorID,
		AuthorName:   p.AuthorName,
		AuthorAvatar: p.AuthorAvatar,
		Title:        p.Title,
		Content:      p.Content,
		ImageColor:   p.ImageColor,
		Likes:        len(p.Likes),
		Comments:     len(p.Comments),
		Timestamp:    p.CreatedAt,
		ImageUrls:    imageUrls,
		IsLiked:      isLiked,
	}
}

// NewPostsHandler returns an http.HandlerFunc for GET/POST /posts backed by SQLite.
// GET  /posts  — returns all posts as []BlogPost (iOS-compatible format)
// POST /posts  — creates a post, persists to SQLite, returns the created BlogPost
// Author identity is read from X-User-Id / X-User-Name / X-User-Avatar request headers.
func NewPostsHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}

		switch r.Method {
		case http.MethodGet:
			var posts []models.Post
			if err := db.
				Preload("Images").
				Preload("Likes").
				Preload("Comments").
				Order("created_at DESC").
				Find(&posts).Error; err != nil {
				http.Error(w, "Failed to fetch posts: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Anonymous viewers get isLiked=false for every post.
			requesterID := strings.TrimSpace(r.Header.Get("X-User-Id"))

			result := make([]models.BlogPost, 0, len(posts))
			for _, p := range posts {
				result = append(result, toBlogPost(p, requesterID))
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)

		case http.MethodPost:
			// Decode body fields: title, content, imageColor, imageUrls
			var body struct {
				Title      string   `json:"title"`
				Content    string   `json:"content"`
				ImageColor string   `json:"imageColor"`
				ImageUrls  []string `json:"imageUrls"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "Invalid request body", http.StatusBadRequest)
				return
			}
			if body.Content == "" && body.Title == "" {
				http.Error(w, "title or content required", http.StatusBadRequest)
				return
			}

			// Author identity comes from headers, NOT body
			authorID := r.Header.Get("X-User-Id")
			authorName := r.Header.Get("X-User-Name")
			authorAvatar := r.Header.Get("X-User-Avatar")
			if authorName == "" {
				authorName = "Anonymous"
			}
			if authorAvatar == "" {
				authorAvatar = "person.circle.fill"
			}
			if authorID == "" {
				authorID = authorName
			}
			imageColor := body.ImageColor
			if imageColor == "" {
				imageColor = "blue"
			}

			post := models.Post{
				AuthorID:     authorID,
				AuthorName:   authorName,
				AuthorAvatar: authorAvatar,
				Title:        body.Title,
				Content:      body.Content,
				ImageColor:   imageColor,
			}
			if err := db.Create(&post).Error; err != nil {
				http.Error(w, "Failed to save post: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Save image URLs as PostImage records
			for i, url := range body.ImageUrls {
				img := models.PostImage{
					PostID:    post.ID,
					ImageURL:  url,
					SortOrder: i,
				}
				db.Create(&img)
			}

			response := models.BlogPost{
				ID:           post.ID,
				AuthorID:     post.AuthorID,
				AuthorName:   post.AuthorName,
				AuthorAvatar: post.AuthorAvatar,
				Title:        post.Title,
				Content:      post.Content,
				ImageColor:   post.ImageColor,
				Likes:        0,
				Comments:     0,
				Timestamp:    post.CreatedAt,
				ImageUrls:    body.ImageUrls,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(response)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// NewPostDetailHandler returns the handler for /posts/{id}.
//   GET    -> single post with images/likes/comments and per-viewer `isLiked`
//   DELETE -> author-only delete (cascades to images/likes/comments via FK constraints)
func NewPostDetailHandler(db *gorm.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}

		postID := strings.TrimSpace(r.PathValue("id"))
		if postID == "" {
			http.Error(w, "post id required", http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			var post models.Post
			if err := db.
				Preload("Images").
				Preload("Likes").
				Preload("Comments").
				First(&post, "id = ?", postID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					http.Error(w, "post not found", http.StatusNotFound)
					return
				}
				http.Error(w, "failed to load post: "+err.Error(), http.StatusInternalServerError)
				return
			}

			requesterID := strings.TrimSpace(r.Header.Get("X-User-Id"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(toBlogPost(post, requesterID))

		case http.MethodDelete:
			requesterID := strings.TrimSpace(r.Header.Get("X-User-Id"))
			if requesterID == "" {
				http.Error(w, "missing X-User-Id", http.StatusUnauthorized)
				return
			}

			var post models.Post
			if err := db.Select("id", "author_id").First(&post, "id = ?", postID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					http.Error(w, "post not found", http.StatusNotFound)
					return
				}
				http.Error(w, "failed to load post: "+err.Error(), http.StatusInternalServerError)
				return
			}

			if post.AuthorID != requesterID {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}

			// Explicit cascade in a transaction so we don't rely on the SQLite FK pragma being on.
			err := db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("post_id = ?", postID).Delete(&models.PostImage{}).Error; err != nil {
					return err
				}
				if err := tx.Where("post_id = ?", postID).Delete(&models.PostLike{}).Error; err != nil {
					return err
				}
				if err := tx.Where("post_id = ?", postID).Delete(&models.PostComment{}).Error; err != nil {
					return err
				}
				return tx.Delete(&post).Error
			})
			if err != nil {
				http.Error(w, "failed to delete post: "+err.Error(), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}
