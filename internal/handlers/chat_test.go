package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/rag"
)

type panicRAGService struct{}

func (panicRAGService) Ask(string) (*rag.Response, error) { panic("boom ask") }
func (panicRAGService) AskWithContext(rag.ChatRequest) (*rag.ChatResponse, error) {
	panic("boom ask with context")
}
func (panicRAGService) GetProviders() (*rag.ProvidersResponse, error) { panic("boom providers") }

func TestChatProvidersHandlerUsesLocalCatalog(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"bluecross", "MSIG"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}

	client := rag.NewClient(&config.Config{
		HKInsuranceRAGDataPath: root,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/chat/providers", nil)
	rec := httptest.NewRecorder()

	NewChatProvidersHandler(client).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var response rag.ProvidersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	providerMap := make(map[string]rag.Provider, len(response.Providers))
	for _, provider := range response.Providers {
		providerMap[provider.ID] = provider
	}

	if got, ok := providerMap["bluecross"]; !ok || !got.HasData {
		t.Fatalf("expected bluecross with has_data=true, got %#v", got)
	}
	if got, ok := providerMap["bolttech"]; !ok || got.HasData {
		t.Fatalf("expected bolttech with has_data=false, got %#v", got)
	}
	if got, ok := providerMap["MSIG"]; !ok || got.Name != "MSIG" || !got.HasData {
		t.Fatalf("expected discovered MSIG provider, got %#v", got)
	}
}

func TestChatAskHandlerUsesLocalGoRuntime(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bluecross"), 0o755); err != nil {
		t.Fatalf("mkdir bluecross: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "bluecross", "blue_cross.md"),
		[]byte("# Payment & Waiting Periods\nBlue Cross waiting period for injury is 7 days."),
		0o644,
	); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	store := chat.NewSessionStore(0)
	client := rag.NewClient(&config.Config{
		HKInsuranceRAGEnabled:  true,
		HKInsuranceRAGDataPath: root,
		HKInsuranceRAGTopK:     3,
	})

	payload, _ := json.Marshal(map[string]string{
		"query": "Blue Cross waiting period for injury?",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/ask", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	NewChatAskHandler(store, client).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var response struct {
		Answer         string   `json:"answer"`
		Sources        []string `json:"sources"`
		ActiveProvider string   `json:"active_provider"`
		SessionID      string   `json:"session_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ActiveProvider != "bluecross" {
		t.Fatalf("expected bluecross active provider, got %q", response.ActiveProvider)
	}
	if len(response.Sources) == 0 || response.Sources[0] != "bluecross (blue_cross.md)" {
		t.Fatalf("unexpected sources: %#v", response.Sources)
	}
	if response.SessionID == "" {
		t.Fatal("expected session id in response")
	}
	if response.Answer == "" {
		t.Fatal("expected non-empty answer")
	}
}

func TestChatAskHandlerRecoversFromPanic(t *testing.T) {
	store := chat.NewSessionStore(0)
	payload, _ := json.Marshal(map[string]string{
		"query": "hello",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/ask", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	NewChatAskHandler(store, panicRAGService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body == "" {
		t.Fatal("expected panic error body")
	}
}
