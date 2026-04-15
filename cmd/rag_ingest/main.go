package main

import (
	"context"
	"fmt"
	"log"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"github.com/wangwuxing777/Pawrd_Backend/internal/services/rag"
)

func main() {
	cfg := config.LoadConfig()
	db, err := models.InitDB(cfg)
	if err != nil {
		log.Fatalf("init db: %v", err)
	}

	client := rag.NewClient(cfg, db)
	if err := client.Rebuild(context.Background()); err != nil {
		log.Fatalf("rebuild rag index: %v", err)
	}

	providers, err := client.GetProviders()
	if err != nil {
		log.Fatalf("list providers: %v", err)
	}

	fmt.Printf("HK insurance RAG ingest complete. providers=%d data_path=%s\n", len(providers.Providers), cfg.HKInsuranceRAGDataPath)
}
