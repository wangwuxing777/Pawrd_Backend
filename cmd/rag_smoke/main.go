package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type smokeCase struct {
	Name         string
	Question     string
	Provider     string
	Language     string
	ExpectedMode string
}

type smokeResult struct {
	AnswerMode string
	Answer     string
	Sources    int
}

func main() {
	goBase := getenv("GO_RAG_URL", "http://127.0.0.1:8012/api/rag/go/query")
	cases := []smokeCase{
		{
			Name:         "prudential_room_and_board_limit",
			Question:     "What is the annual limit for Prudential room and board?",
			Provider:     "prudential",
			Language:     "en",
			ExpectedMode: "deterministic_benefit_limit_single",
		},
		{
			Name:         "bluecross_consult_coverage_zh",
			Question:     "Blue Cross 包唔包獸醫診症？",
			Provider:     "bluecross",
			Language:     "zh",
			ExpectedMode: "deterministic_consult_coverage_single",
		},
		{
			Name:         "bluecross_vs_prudential_consult_limit",
			Question:     "Compare Blue Cross and Prudential veterinary consultation limits.",
			ExpectedMode: "deterministic_consult_limit_comparison",
		},
		{
			Name:         "waiting_period_meaning",
			Question:     "What is the meaning of waiting period?",
			ExpectedMode: "deterministic_waiting_period_single",
		},
	}

	client := &http.Client{Timeout: 90 * time.Second}
	failed := false
	fmt.Printf("Running %d Go RAG smoke case(s) against %s\n\n", len(cases), goBase)

	for _, c := range cases {
		res, err := query(client, goBase, c)
		if err != nil {
			failed = true
			fmt.Printf("[FAIL] %s\n", c.Name)
			fmt.Printf("  error: %v\n\n", err)
			continue
		}

		pass := strings.TrimSpace(res.Answer) != "" &&
			res.AnswerMode == c.ExpectedMode &&
			res.Sources > 0
		if !pass {
			failed = true
		}

		status := "PASS"
		if !pass {
			status = "FAIL"
		}
		fmt.Printf("[%s] %s\n", status, c.Name)
		fmt.Printf("  expected_mode: %s\n", c.ExpectedMode)
		fmt.Printf("  actual_mode:   %s\n", res.AnswerMode)
		fmt.Printf("  sources:       %d\n", res.Sources)
		fmt.Printf("  answer:        %s\n\n", oneLine(res.Answer))
	}

	if failed {
		os.Exit(2)
	}
}

func query(client *http.Client, base string, c smokeCase) (smokeResult, error) {
	u, err := url.Parse(base)
	if err != nil {
		return smokeResult{}, err
	}
	q := u.Query()
	q.Set("q", c.Question)
	q.Set("max_sources", "3")
	if strings.TrimSpace(c.Provider) != "" {
		q.Set("provider", c.Provider)
	}
	if strings.TrimSpace(c.Language) != "" {
		q.Set("language", c.Language)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return smokeResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return smokeResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return smokeResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return smokeResult{}, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return smokeResult{}, err
	}
	res := smokeResult{
		AnswerMode: stringValue(payload, "answer_mode"),
		Answer:     stringValue(payload, "answer"),
	}
	if arr, ok := payload["sources"].([]any); ok {
		res.Sources = len(arr)
	}
	return res, nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func stringValue(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func getenv(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
