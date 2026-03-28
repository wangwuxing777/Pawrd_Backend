package rag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
)

// Client communicates with the Python RAG FastAPI service.
type Client struct {
	Config *config.Config
}

// Request is the old simple request (kept for backward compatibility).
type Request struct {
	Query string `json:"query"`
}

// ChatRequest is the full request format matching the updated RAG API.
type ChatRequest struct {
	Query       string          `json:"query"`
	Provider    string          `json:"provider,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	ChatHistory []chat.ChatTurn `json:"chat_history,omitempty"`
}

// Response is the old simple response (kept for backward compatibility).
type Response struct {
	Answer  string   `json:"answer"`
	Sources []string `json:"sources"`
}

// ChatResponse is the full response format from the updated RAG API.
type ChatResponse struct {
	Answer         string   `json:"answer"`
	Sources        []string `json:"sources"`
	ActiveProvider string   `json:"active_provider,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
}

// Provider represents a single insurance provider.
type Provider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ProvidersResponse is the response from GET /providers.
type ProvidersResponse struct {
	Providers []Provider `json:"providers"`
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		Config: cfg,
	}
}

// Ask sends a simple query (backward compatible).
func (c *Client) Ask(query string) (*Response, error) {
	reqBody := Request{Query: query}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := http.Post(c.Config.RAGServiceURL+"/ask", "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to call RAG service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("RAG service returned status %d: %s", resp.StatusCode, string(body))
	}

	var ragResp Response
	if err := json.NewDecoder(resp.Body).Decode(&ragResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &ragResp, nil
}

// AskWithContext sends a query with session context and chat history.
func (c *Client) AskWithContext(req ChatRequest) (*ChatResponse, error) {
	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := http.Post(c.Config.RAGServiceURL+"/ask", "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to call RAG service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("RAG service returned status %d: %s", resp.StatusCode, string(body))
	}

	var ragResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&ragResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &ragResp, nil
}

// GetProviders fetches the list of available providers from the RAG service.
func (c *Client) GetProviders() (*ProvidersResponse, error) {
	resp, err := http.Get(c.Config.RAGServiceURL + "/providers")
	if err != nil {
		return nil, fmt.Errorf("failed to call RAG providers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("RAG providers returned status %d: %s", resp.StatusCode, string(body))
	}

	var result ProvidersResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode providers: %w", err)
	}

	return &result, nil
}
