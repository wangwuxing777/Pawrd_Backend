package models

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// PartnerType 合作商家类型
type PartnerType string

const (
	PartnerTypeClinic    PartnerType = "clinic"    // 诊所
	PartnerTypeShop      PartnerType = "shop"       // 宠物用品店
	PartnerTypeGrooming  PartnerType = "grooming"  // 美容/洗澡
	PartnerTypeBoarding  PartnerType = "boarding"  // 寄宿
)

// PartnerStatus 申请状态
type PartnerStatus string

const (
	PartnerStatusPending  PartnerStatus = "pending"  // 待审核
	PartnerStatusApproved PartnerStatus = "approved" // 已批准
	PartnerStatusRejected PartnerStatus = "rejected" // 已拒绝
)

// Partner 合作商家申请表
type Partner struct {
	ID           uint          `json:"id" gorm:"primaryKey;autoIncrement"`
	BusinessName string        `json:"business_name" gorm:"not null"`
	ContactName  string        `json:"contact_name" gorm:"not null"`
	ContactEmail string        `json:"contact_email" gorm:"not null"`
	ContactPhone string        `json:"contact_phone"`
	PartnerType  PartnerType   `json:"partner_type" gorm:"not null"`
	District     string        `json:"district"`     // 地区（如：中西区、湾仔区）
	Message      string        `json:"message"`      // 商家补充说明
	Status       PartnerStatus `json:"status" gorm:"default:'pending'"`
	APIKey       string        `json:"api_key,omitempty" gorm:"uniqueIndex"`
	AdminNote    string        `json:"admin_note,omitempty"` // 内部审核备注
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// GenerateAPIKey 生成随机 API Key
func GenerateAPIKey() (string, error) {
	b := make([]byte, 24)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return "pw_" + hex.EncodeToString(b), nil
}
