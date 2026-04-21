package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
)

type openAIEmbedder struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func newOpenAIEmbedder(cfg *config.Config) *openAIEmbedder {
	if strings.TrimSpace(cfg.HKInsuranceRAGEmbeddingAPIKey) == "" {
		return nil
	}
	return &openAIEmbedder{
		baseURL: strings.TrimRight(cfg.HKInsuranceRAGEmbeddingBaseURL, "/"),
		apiKey:  cfg.HKInsuranceRAGEmbeddingAPIKey,
		model:   cfg.HKInsuranceRAGEmbeddingModel,
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

func (e *openAIEmbedder) EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return [][]float64{}, nil
	}
	embeddings := make([][]float64, len(texts))
	const batchSize = 32
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		payload := map[string]any{
			"model": e.model,
			"input": texts[start:end],
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)

		resp, err := e.httpClient.Do(req)
		if err != nil {
			return nil, err
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("embedding request failed: status %d body=%s", resp.StatusCode, trimBody(respBody))
		}

		var decoded struct {
			Data []struct {
				Index     int       `json:"index"`
				Embedding []float64 `json:"embedding"`
			} `json:"data"`
		}
		if err := json.Unmarshal(respBody, &decoded); err != nil {
			return nil, err
		}

		for _, item := range decoded.Data {
			targetIndex := start + item.Index
			if targetIndex >= 0 && targetIndex < len(embeddings) {
				embeddings[targetIndex] = item.Embedding
			}
		}
	}
	return embeddings, nil
}

type openAICompleter struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func newOpenAICompleter(cfg *config.Config) *openAICompleter {
	if strings.TrimSpace(cfg.HKInsuranceRAGLLMAPIKey) == "" {
		return nil
	}
	return &openAICompleter{
		baseURL: strings.TrimRight(cfg.HKInsuranceRAGLLMBaseURL, "/"),
		apiKey:  cfg.HKInsuranceRAGLLMAPIKey,
		model:   cfg.HKInsuranceRAGLLMModel,
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

func (c *openAICompleter) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": 0.1,
		"max_tokens":  1024,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("chat completion failed: status %d body=%s", resp.StatusCode, trimBody(respBody))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content          any `json:"content"`
				ReasoningContent any `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return "", err
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("chat completion returned no choices")
	}
	return extractPrimaryMessageContent(
		decoded.Choices[0].Message.Content,
		decoded.Choices[0].Message.ReasoningContent,
	), nil
}

func extractPrimaryMessageContent(content any, reasoning any) string {
	if text := extractMessageContent(content); strings.TrimSpace(text) != "" {
		return text
	}
	return extractMessageContent(reasoning)
}

func extractMessageContent(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				if text, ok := obj["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func trimBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 400 {
		return text[:400] + "..."
	}
	return text
}
