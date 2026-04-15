package rag

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wangwuxing777/Pawrd_Backend/internal/models"
	"gorm.io/gorm"
)

type dbStore struct {
	db *gorm.DB
}

func newDBStore(db *gorm.DB) *dbStore {
	if db == nil {
		return nil
	}
	return &dbStore{db: db}
}

func (s *dbStore) LoadChunks(ctx context.Context) ([]indexedChunk, error) {
	var records []models.RagChunk
	if err := s.db.WithContext(ctx).Order("provider asc, source_name asc, chunk_index asc").Find(&records).Error; err != nil {
		return nil, err
	}
	chunks := make([]indexedChunk, 0, len(records))
	for _, record := range records {
		var embedding []float64
		if strings.TrimSpace(record.EmbeddingJSON) != "" {
			_ = json.Unmarshal([]byte(record.EmbeddingJSON), &embedding)
		}
		chunks = append(chunks, indexedChunk{
			ID:        record.ID,
			Provider:  record.Provider,
			Source:    record.SourceName,
			Language:  record.Language,
			Section:   record.SectionPath,
			Text:      record.Body,
			Embedding: embedding,
		})
	}
	return chunks, nil
}

func (s *dbStore) ChunkCount(ctx context.Context) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&models.RagChunk{}).Count(&count).Error
	return count, err
}

func (s *dbStore) Rebuild(ctx context.Context, dataPath string, docs []documentRecord) error {
	manifestHash := buildManifestHash(docs)
	run := models.RagIngestRun{
		Status:        "running",
		DataPath:      dataPath,
		FileCount:     len(docs),
		DocumentCount: len(docs),
	}
	if err := s.db.WithContext(ctx).Create(&run).Error; err != nil {
		return err
	}

	tx := s.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}

	if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.RagChunk{}).Error; err != nil {
		tx.Rollback()
		s.failRun(ctx, run.ID, err)
		return err
	}
	if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&models.RagDocument{}).Error; err != nil {
		tx.Rollback()
		s.failRun(ctx, run.ID, err)
		return err
	}

	chunkCount := 0
	for _, doc := range docs {
		document := models.RagDocument{
			Provider:    doc.Provider,
			SourcePath:  doc.SourcePath,
			SourceName:  doc.SourceName,
			Language:    doc.Language,
			DocType:     doc.DocType,
			ContentHash: doc.ContentHash,
		}
		if err := tx.Create(&document).Error; err != nil {
			tx.Rollback()
			s.failRun(ctx, run.ID, err)
			return err
		}
		for idx, chunk := range doc.Chunks {
			embeddingJSON, err := json.Marshal(chunk.Embedding)
			if err != nil {
				tx.Rollback()
				s.failRun(ctx, run.ID, err)
				return err
			}
			record := models.RagChunk{
				ID:            chunk.ID,
				DocumentID:    document.ID,
				Provider:      chunk.Provider,
				SourceName:    chunk.Source,
				Language:      chunk.Language,
				SectionPath:   chunk.Section,
				ChunkIndex:    idx,
				Body:          chunk.Text,
				BodyHash:      hashText(chunk.Text),
				EmbeddingJSON: string(embeddingJSON),
			}
			if err := tx.Create(&record).Error; err != nil {
				tx.Rollback()
				s.failRun(ctx, run.ID, err)
				return err
			}
			chunkCount++
		}
	}

	if err := tx.Commit().Error; err != nil {
		s.failRun(ctx, run.ID, err)
		return err
	}

	now := time.Now()
	return s.db.WithContext(ctx).Model(&models.RagIngestRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"status":        "completed",
		"manifest_hash": manifestHash,
		"chunk_count":   chunkCount,
		"completed_at":  &now,
	}).Error
}

func (s *dbStore) failRun(ctx context.Context, id string, err error) {
	now := time.Now()
	_ = s.db.WithContext(ctx).Model(&models.RagIngestRun{}).Where("id = ?", id).Updates(map[string]any{
		"status":        "failed",
		"error_summary": err.Error(),
		"completed_at":  &now,
	}).Error
}

type documentRecord struct {
	Provider    string
	SourcePath  string
	SourceName  string
	Language    string
	DocType     string
	ContentHash string
	Chunks      []indexedChunk
}

func buildManifestHash(docs []documentRecord) string {
	parts := make([]string, 0, len(docs))
	for _, doc := range docs {
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%s", doc.Provider, filepath.Base(doc.SourcePath), doc.Language, doc.ContentHash))
	}
	sort.Strings(parts)
	return hashText(strings.Join(parts, "\n"))
}

func hashText(text string) string {
	sum := sha1.Sum([]byte(text))
	return hex.EncodeToString(sum[:])
}

func scanManifest(dataPath string) (string, error) {
	items := make([]string, 0, 32)
	err := filepath.Walk(dataPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(dataPath, path)
		if relErr != nil {
			rel = path
		}
		items = append(items, rel+"|"+hashText(string(content)))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(items)
	return hashText(strings.Join(items, "\n")), nil
}
