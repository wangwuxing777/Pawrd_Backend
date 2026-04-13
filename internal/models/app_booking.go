package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AppBookingMirror stores the app-facing booking mirror owned by R1.
// It is intentionally consumer-facing metadata only, never merchant truth.
type AppBookingMirror struct {
	ID                string     `gorm:"type:text;primaryKey" json:"id"`
	ExternalBookingID string     `gorm:"type:text;uniqueIndex;not null" json:"external_booking_id"`
	ClinicID          string     `gorm:"type:text;index;not null" json:"clinic_id"`
	BookingClinicID   string     `gorm:"type:text;index;default:''" json:"-"`
	ClinicName        string     `gorm:"type:text;default:''" json:"clinic_name"`
	ServiceType       string     `gorm:"type:text;index;not null" json:"service_type"`
	ScheduledDate     time.Time  `gorm:"index;not null" json:"scheduled_date"`
	Status            string     `gorm:"type:text;index;not null" json:"status"`
	MerchantStatus    string     `gorm:"type:text;default:''" json:"merchant_status"`
	MerchantInternalAppointmentID *uint `gorm:"index" json:"-"`
	MerchantUpdatedAt *time.Time `gorm:"index" json:"-"`
	LastSyncAttemptAt *time.Time `gorm:"index" json:"-"`
	LastSyncedAt      *time.Time `gorm:"index" json:"-"`
	LastSyncError     string     `gorm:"type:text;default:''" json:"-"`
	LastSyncSource    string     `gorm:"type:text;default:''" json:"-"`
	RequestID         string     `gorm:"type:text;index;default:''" json:"-"`
	IdempotencyKey    string     `gorm:"type:text;index;default:''" json:"-"`
	Notes             string     `gorm:"type:text;default:''" json:"notes"`
	PetID             string     `gorm:"type:text;index;not null" json:"pet_id"`
	PetName           string     `gorm:"type:text;default:''" json:"pet_name"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (a *AppBookingMirror) BeforeCreate(tx *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	return nil
}
