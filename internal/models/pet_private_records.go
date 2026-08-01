package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type PetVaccination struct {
	ID     string    `gorm:"type:text;primaryKey" json:"id"`
	PetID  string    `gorm:"type:text;not null;index" json:"pet_id"`
	Name   string    `gorm:"type:text;not null" json:"name"`
	Date   time.Time `json:"date"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (v *PetVaccination) BeforeCreate(tx *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	return nil
}

type PetMedicalVisit struct {
	ID        string    `gorm:"type:text;primaryKey" json:"id"`
	PetID     string    `gorm:"type:text;not null;index" json:"pet_id"`
	Date      time.Time `json:"date"`
	Clinic    string    `gorm:"type:text;default:''" json:"clinic"`
	Vet       string    `gorm:"type:text;default:''" json:"vet"`
	Reason    string    `gorm:"type:text;default:''" json:"reason"`
	Diagnosis string    `gorm:"type:text;default:''" json:"diagnosis"`
	Treatment string    `gorm:"type:text;default:''" json:"treatment"`
	Cost      float64   `gorm:"type:real;default:0" json:"cost"`
	Notes     string    `gorm:"type:text;default:''" json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (v *PetMedicalVisit) BeforeCreate(tx *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	return nil
}

type PetMedication struct {
	ID        string     `gorm:"type:text;primaryKey" json:"id"`
	PetID     string     `gorm:"type:text;not null;index" json:"pet_id"`
	Name      string     `gorm:"type:text;not null" json:"name"`
	Dosage    string     `gorm:"type:text;default:''" json:"dosage"`
	Frequency string     `gorm:"type:text;default:''" json:"frequency"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
	Notes     string     `gorm:"type:text;default:''" json:"notes"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (v *PetMedication) BeforeCreate(tx *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	return nil
}

type PetWeightEntry struct {
	ID        string    `gorm:"type:text;primaryKey" json:"id"`
	PetID     string    `gorm:"type:text;not null;index" json:"pet_id"`
	Date      time.Time `json:"date"`
	WeightKg  float64   `gorm:"type:real;not null" json:"weight_kg"`
	Notes     string    `gorm:"type:text;default:''" json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (v *PetWeightEntry) BeforeCreate(tx *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	return nil
}
