package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/services/chat"
)

func TestRAGHandlerRecoversFromPanic(t *testing.T) {
	store := chat.NewSessionStore(0)
	payload, _ := json.Marshal(map[string]string{
		"query": "hello",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/chat", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	NewRAGHandler(panicRAGService{}, store).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body == "" {
		t.Fatal("expected panic error body")
	}
}
