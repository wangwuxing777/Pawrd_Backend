package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wangwuxing777/Pawrd_Backend/internal/config"
	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDBStoreRebuildAndLoad(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&models.RagDocument{}, &models.RagChunk{}, &models.RagIngestRun{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "bluecross", "blue_cross.md"), "# Waiting Period\nBlue Cross waiting period for injury is 7 days.")

	runtime := newLocalRuntime(&config.Config{
		HKInsuranceRAGDataPath: root,
		HKInsuranceRAGTopK:     3,
	}, db, fakeEmbedder{}, fakeCompleter{})

	if err := runtime.rebuildStore(context.Background()); err != nil {
		t.Fatalf("rebuildStore: %v", err)
	}

	store := newDBStore(db)
	count, err := store.ChunkCount(context.Background())
	if err != nil {
		t.Fatalf("ChunkCount: %v", err)
	}
	if count == 0 {
		t.Fatal("expected chunks to be persisted")
	}

	loaded, err := store.LoadChunks(context.Background())
	if err != nil {
		t.Fatalf("LoadChunks: %v", err)
	}
	if len(loaded) == 0 {
		t.Fatal("expected non-empty chunk load")
	}
	if loaded[0].Provider != "bluecross" {
		t.Fatalf("expected bluecross provider, got %q", loaded[0].Provider)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
