package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/rag"
)

// legacyChatRequest matches what the iOS frontend currently sends.
// Supports both old format (just query) and new format (with session_id + provider).
type legacyChatRequest struct {
	Query     string `json:"query"`
	Provider  string `json:"provider,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// legacyChatResponse matches what the iOS frontend expects.
type legacyChatResponse struct {
	Answer         string   `json:"answer"`
	Sources        []string `json:"sources"`
	ActiveProvider string   `json:"active_provider,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
}

// NewRAGHandler handles POST /api/chat with automatic session management.
// The frontend only needs to send { query, session_id? } — the backend handles everything else.
func NewRAGHandler(client rag.Service, store *chat.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req legacyChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if client == nil {
			http.Error(w, "RAG client not initialized", http.StatusInternalServerError)
			return
		}

		// Session management: create or load
		var session *chat.Session
		if req.SessionID != "" {
			session = store.Get(req.SessionID)
		}
		if session == nil {
			session = store.Create()
			log.Printf("[RAG] Created new session: %s", session.ID)
		}

		// Resolve provider using priority chain
		resolvedProvider := chat.ResolveProvider(session, req.Query)

		// Override with explicit provider if provided in request
		if req.Provider != "" {
			resolvedProvider = req.Provider
		}

		log.Printf("[RAG] Session %s: query='%s', provider='%s', history_turns=%d",
			session.ID, req.Query, resolvedProvider, len(session.ChatHistory))

		// Build full request with session context
		ragReq := rag.ChatRequest{
			Query:       req.Query,
			Provider:    resolvedProvider,
			SessionID:   session.ID,
			ChatHistory: session.LastNTurns(10), // Last 5 pairs
		}

		// Call RAG service
		resp, err := client.AskWithContext(ragReq)
		if err != nil {
			log.Printf("[RAG] Error: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Save this turn to session history
		session.ChatHistory = append(session.ChatHistory, chat.ChatTurn{
			Role:    "user",
			Content: req.Query,
		})
		session.ChatHistory = append(session.ChatHistory, chat.ChatTurn{
			Role:    "assistant",
			Content: resp.Answer,
		})

		// Update last mentioned provider if detected
		detected := chat.DetectProvider(req.Query)
		if detected != "" {
			session.LastMentionedProvider = detected
		}

		store.Update(session)

		// Return response with session info
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(legacyChatResponse{
			Answer:         resp.Answer,
			Sources:        resp.Sources,
			ActiveProvider: resp.ActiveProvider,
			SessionID:      session.ID,
		})
	}
}
