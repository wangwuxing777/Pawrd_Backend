package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
)

type medicalAIClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func newMedicalAIClient(cfg *config.Config) *medicalAIClient {
	timeout := time.Duration(cfg.RAGLLMTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &medicalAIClient{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.RAGLLMBaseURL), "/"),
		apiKey:  strings.TrimSpace(cfg.RAGLLMAPIKey),
		model:   strings.TrimSpace(cfg.RAGLLMModel),
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *medicalAIClient) configured() bool {
	return c != nil && c.baseURL != "" && c.apiKey != "" && c.model != ""
}

func (c *medicalAIClient) summarize(query string) (string, error) {
	if !c.configured() {
		return "", fmt.Errorf("medical AI is not configured")
	}
	payload := map[string]any{
		"model":       c.model,
		"temperature": 0.1,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": strings.Join([]string{
					"You are a veterinary medical information assistant.",
					"Analyze only the pet profile and health-report text supplied by the user.",
					"Extract diagnoses, abnormal findings, ongoing conditions, medications, allergies, and clinically relevant uncertainties.",
					"Use the same language as the user when possible.",
					"Be concise and factual; do not invent missing findings.",
					"Do not recommend insurance products and do not use insurance policy knowledge.",
					"State clearly when the available report is insufficient.",
					"Flag urgent red signs that warrant prompt veterinary care, but do not claim to replace a licensed veterinarian.",
				}, " "),
			},
			{
				"role":    "user",
				"content": strings.TrimSpace(query),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if len(msg) > 300 {
			msg = msg[:300] + "..."
		}
		return "", fmt.Errorf("medical AI status %d body=%s", resp.StatusCode, msg)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("medical AI returned empty content")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}
