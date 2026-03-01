package models

import (
	"time"

	"gorm.io/gorm"
)

// AuthUser is the authentication user stored in users.db
type AuthUser struct {
	ID           uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Email        string    `json:"email" gorm:"uniqueIndex;not null"`
	Phone        string    `json:"phone" gorm:"uniqueIndex;not null"`
	PasswordHash string    `json:"-" gorm:"not null"`
	Name         string    `json:"name" gorm:"not null"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuthDB holds a reference to the users database
var AuthDB *gorm.DB
