package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ShippingAddress is the cross-device, JWT-owned address book entry used by
// Pawrd checkout and profile delivery details.
type ShippingAddress struct {
	ID            string    `gorm:"type:text;primaryKey" json:"id"`
	UserID        string    `gorm:"type:text;not null;index" json:"user_id"`
	Label         string    `gorm:"type:text;default:''" json:"label"`
	RecipientName string    `gorm:"type:text;not null" json:"recipient_name"`
	Phone         string    `gorm:"type:text;not null" json:"phone"`
	Address       string    `gorm:"type:text;not null" json:"address"`
	RegionID      string    `gorm:"type:text;not null" json:"region_id"`
	DistrictID    string    `gorm:"type:text;not null" json:"district_id"`
	SortOrder     int       `gorm:"not null;default:0" json:"sort_order"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (s *ShippingAddress) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	return nil
}
