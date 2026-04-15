package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/rag"
)

type evalCase struct {
	Name             string `json:"name"`
	Query            string `json:"query"`
	ExpectedProvider string `json:"expected_provider,omitempty"`
}

type evalResult struct {
	Name             string   `json:"name"`
	Query            string   `json:"query"`
	ExpectedProvider string   `json:"expected_provider,omitempty"`
	ActiveProvider   string   `json:"active_provider,omitempty"`
	Sources          []string `json:"sources"`
	AnswerPreview    string   `json:"answer_preview"`
}

func main() {
	casesPath := flag.String("cases", "assets/rag/hk_insurance_eval.json", "path to eval cases JSON")
	flag.Parse()

	cfg := config.LoadConfig()
	db, err := models.InitDB(cfg)
	if err != nil {
		log.Fatalf("init db: %v", err)
	}
	client := rag.NewClient(cfg, db)

	cases, err := loadCases(*casesPath)
	if err != nil {
		log.Fatalf("load cases: %v", err)
	}

	results := make([]evalResult, 0, len(cases))
	for _, c := range cases {
		resp, err := client.AskWithContext(rag.ChatRequest{Query: c.Query})
		if err != nil {
			log.Fatalf("query %q failed: %v", c.Query, err)
		}
		preview := resp.Answer
		if len([]rune(preview)) > 160 {
			preview = string([]rune(preview)[:160])
		}
		results = append(results, evalResult{
			Name:             c.Name,
			Query:            c.Query,
			ExpectedProvider: c.ExpectedProvider,
			ActiveProvider:   resp.ActiveProvider,
			Sources:          resp.Sources,
			AnswerPreview:    strings.TrimSpace(preview),
		})
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		log.Fatalf("encode results: %v", err)
	}

	fmt.Fprintf(os.Stderr, "Evaluated %d HK insurance RAG cases\n", len(results))
}

func loadCases(path string) ([]evalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}
