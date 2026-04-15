package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/rag"
)

// chatAskRequest is what the frontend sends to POST /api/chat/ask
type chatAskRequest struct {
	SessionID string `json:"session_id"`
	Query     string `json:"query"`
}

// chatAskResponse is what the frontend receives from POST /api/chat/ask
type chatAskResponse struct {
	Answer         string   `json:"answer"`
	Sources        []string `json:"sources"`
	ActiveProvider string   `json:"active_provider,omitempty"`
	SessionID      string   `json:"session_id"`
}

// selectProviderRequest is what the frontend sends to POST /api/chat/session/{id}/provider
type selectProviderRequest struct {
	Provider string `json:"provider"` // "" or null = all providers
}

// sessionResponse is the response for POST /api/chat/session
type sessionResponse struct {
	SessionID string `json:"session_id"`
}

// ---- Handlers ----

// NewChatSessionHandler creates a new chat session.
// POST /api/chat/session → { session_id }
func NewChatSessionHandler(store *chat.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		session := store.Create()
		log.Printf("[Chat] Created session: %s", session.ID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessionResponse{SessionID: session.ID})
	}
}

// NewChatSelectProviderHandler sets the active provider for a session.
// POST /api/chat/session/{id}/provider → 200 OK
func NewChatSelectProviderHandler(store *chat.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Extract session ID from URL path: /api/chat/session/{id}/provider
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/chat/session/"), "/")
		if len(parts) < 2 || parts[1] != "provider" {
			http.Error(w, "Invalid URL format. Expected /api/chat/session/{id}/provider", http.StatusBadRequest)
			return
		}
		sessionID := parts[0]

		session := store.Get(sessionID)
		if session == nil {
			http.Error(w, "Session not found or expired", http.StatusNotFound)
			return
		}

		var req selectProviderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// If provider changed, clear chat history to avoid cross-provider confusion
		if session.SelectedProvider != req.Provider {
			session.ChatHistory = make([]chat.ChatTurn, 0)
			session.LastMentionedProvider = ""
			log.Printf("[Chat] Session %s: provider changed to '%s', history cleared", sessionID, req.Provider)
		}

		session.SelectedProvider = req.Provider
		store.Update(session)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"provider": req.Provider,
		})
	}
}

// NewChatProvidersHandler returns the list of available insurance providers.
// GET /api/chat/providers → { providers: [...] }
func NewChatProvidersHandler(ragService rag.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		providers, err := ragService.GetProviders()
		if err != nil {
			log.Printf("[Chat] Error fetching providers: %v", err)
			http.Error(w, fmt.Sprintf("Failed to fetch providers: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(providers)
	}
}

// NewChatAskHandler handles the main chat query with session context.
// POST /api/chat/ask → { answer, sources, active_provider, session_id }
func NewChatAskHandler(store *chat.SessionStore, ragService rag.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		EnableCors(&w)
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req chatAskRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.Query == "" {
			http.Error(w, "Query cannot be empty", http.StatusBadRequest)
			return
		}

		// Load session (optional — works without session too)
		var session *chat.Session
		if req.SessionID != "" {
			session = store.Get(req.SessionID)
			if session == nil {
				// Session expired or not found — create a new one
				session = store.Create()
				log.Printf("[Chat] Session %s expired, created new: %s", req.SessionID, session.ID)
			}
		} else {
			// No session ID provided — create one
			session = store.Create()
			log.Printf("[Chat] No session provided, created new: %s", session.ID)
		}

		// Resolve provider using priority chain
		resolvedProvider := chat.ResolveProvider(session, req.Query)
		log.Printf("[Chat] Session %s: query='%s', resolved_provider='%s'", session.ID, req.Query, resolvedProvider)

		// Build RAG request with full context
		ragReq := rag.ChatRequest{
			Query:       req.Query,
			Provider:    resolvedProvider,
			SessionID:   session.ID,
			ChatHistory: session.LastNTurns(10), // Last 5 pairs = 10 turns
		}

		// Call RAG service
		ragResp, err := ragService.AskWithContext(ragReq)
		if err != nil {
			log.Printf("[Chat] RAG error: %v", err)
			http.Error(w, fmt.Sprintf("RAG service error: %v", err), http.StatusInternalServerError)
			return
		}

		// Save turn to session history
		session.ChatHistory = append(session.ChatHistory, chat.ChatTurn{
			Role:    "user",
			Content: req.Query,
		})
		session.ChatHistory = append(session.ChatHistory, chat.ChatTurn{
			Role:    "assistant",
			Content: ragResp.Answer,
		})

		// Update last mentioned provider if detected in query
		detected := chat.DetectProvider(req.Query)
		if detected != "" {
			session.LastMentionedProvider = detected
		}

		store.Update(session)

		// Return response
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(chatAskResponse{
			Answer:         ragResp.Answer,
			Sources:        ragResp.Sources,
			ActiveProvider: ragResp.ActiveProvider,
			SessionID:      session.ID,
		})
	}
}
