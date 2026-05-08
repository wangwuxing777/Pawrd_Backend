package models

import "time"

type FriendRequest struct {
	ID          uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	SenderID    uint      `json:"sender_id" gorm:"not null;index"`
	ReceiverID  uint      `json:"receiver_id" gorm:"not null;index"`
	Status      string    `json:"status" gorm:"default:'pending';index"` // pending, accepted, rejected
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Friend struct {
	ID             uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	UserID         uint      `json:"user_id" gorm:"not null;index"`
	FriendUserID   uint      `json:"friend_user_id" gorm:"not null;index"`
	FriendshipDate time.Time `json:"friendship_date"`
}
