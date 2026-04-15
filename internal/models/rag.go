package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type RagDocument struct {
	ID          string    `gorm:"type:text;primaryKey" json:"id"`
	Provider    string    `gorm:"type:text;index;not null" json:"provider"`
	SourcePath  string    `gorm:"type:text;uniqueIndex;not null" json:"source_path"`
	SourceName  string    `gorm:"type:text;index;not null" json:"source_name"`
	Language    string    `gorm:"type:text;index;not null" json:"language"`
	DocType     string    `gorm:"type:text;index;default:'policy'" json:"doc_type"`
	ContentHash string    `gorm:"type:text;index;not null" json:"content_hash"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (d *RagDocument) BeforeCreate(tx *gorm.DB) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	return nil
}

type RagChunk struct {
	ID            string    `gorm:"type:text;primaryKey" json:"id"`
	DocumentID    string    `gorm:"type:text;index;not null" json:"document_id"`
	Provider      string    `gorm:"type:text;index;not null" json:"provider"`
	SourceName    string    `gorm:"type:text;index;not null" json:"source_name"`
	Language      string    `gorm:"type:text;index;not null" json:"language"`
	SectionPath   string    `gorm:"type:text;default:''" json:"section_path"`
	ChunkIndex    int       `gorm:"index;not null" json:"chunk_index"`
	Body          string    `gorm:"type:text;not null" json:"body"`
	BodyHash      string    `gorm:"type:text;index;not null" json:"body_hash"`
	EmbeddingJSON string    `gorm:"type:text;not null;default:'[]'" json:"embedding_json"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (c *RagChunk) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}

type RagIngestRun struct {
	ID            string     `gorm:"type:text;primaryKey" json:"id"`
	Status        string     `gorm:"type:text;index;not null" json:"status"`
	DataPath      string     `gorm:"type:text;not null" json:"data_path"`
	FileCount     int        `gorm:"not null;default:0" json:"file_count"`
	DocumentCount int        `gorm:"not null;default:0" json:"document_count"`
	ChunkCount    int        `gorm:"not null;default:0" json:"chunk_count"`
	ManifestHash  string     `gorm:"type:text;index;default:''" json:"manifest_hash"`
	ErrorSummary  string     `gorm:"type:text;default:''" json:"error_summary"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func (r *RagIngestRun) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	return nil
}
