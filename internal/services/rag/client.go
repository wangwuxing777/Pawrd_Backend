package rag

import (
	"context"
	"fmt"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/providercatalog"
	"gorm.io/gorm"
)

// Client serves the Go-owned HK insurance RAG path.
type Client struct {
	Config  *config.Config
	runtime *localRuntime
}

type Service interface {
	Ask(query string) (*Response, error)
	AskWithContext(req ChatRequest) (*ChatResponse, error)
	GetProviders() (*ProvidersResponse, error)
}

// ChatRequest is the full request format matching the updated RAG API.
type ChatRequest struct {
	Query       string          `json:"query"`
	Provider    string          `json:"provider,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	ChatHistory []chat.ChatTurn `json:"chat_history,omitempty"`
}

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
	ID      string `json:"id"`
	Name    string `json:"name"`
	HasData bool   `json:"has_data"`
}

// ProvidersResponse is the response from GET /providers.
type ProvidersResponse struct {
	Providers []Provider `json:"providers"`
}

func NewClient(cfg *config.Config, db ...*gorm.DB) *Client {
	var database *gorm.DB
	if len(db) > 0 {
		database = db[0]
	}
	return &Client{
		Config:  cfg,
		runtime: newLocalRuntime(cfg, database, nil, nil),
	}
}

func (c *Client) Rebuild(ctx context.Context) error {
	if c.runtime == nil {
		return nil
	}
	return c.runtime.Rebuild(ctx)
}

// Ask sends a simple query through the Go runtime.
func (c *Client) Ask(query string) (*Response, error) {
	if c.Config == nil || !c.Config.HKInsuranceRAGEnabled {
		return nil, fmt.Errorf("HK insurance Go RAG is disabled")
	}
	resp, err := c.runtime.AskWithContext(context.Background(), ChatRequest{Query: query})
	if err != nil {
		return nil, err
	}
	return &Response{Answer: resp.Answer, Sources: resp.Sources}, nil
}

// AskWithContext sends a query with session context and chat history through
// the Go runtime.
func (c *Client) AskWithContext(req ChatRequest) (*ChatResponse, error) {
	if c.Config == nil || !c.Config.HKInsuranceRAGEnabled {
		return nil, fmt.Errorf("HK insurance Go RAG is disabled")
	}
	return c.runtime.AskWithContext(context.Background(), req)
}

// GetProviders returns the current provider catalog from the Go-owned source corpus.
func (c *Client) GetProviders() (*ProvidersResponse, error) {
	providerList := providercatalog.BuildProviderList(c.Config.HKInsuranceRAGDataPath)
	result := &ProvidersResponse{
		Providers: make([]Provider, 0, len(providerList)),
	}
	for _, provider := range providerList {
		result.Providers = append(result.Providers, Provider{
			ID:      provider.ID,
			Name:    provider.Name,
			HasData: provider.HasData,
		})
	}
	return result, nil
}
