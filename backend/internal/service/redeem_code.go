package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type RedeemCode struct {
	ID        int64
	Code      string
	Type      string
	Value     float64
	Status    string
	UsedBy    *int64
	UsedAt    *time.Time
	Notes     string
	CreatedAt time.Time
	ExpiresAt *time.Time

	GroupID      *int64
	ValidityDays int

	User  *User
	Group *Group
}

func (r *RedeemCode) IsUsed() bool {
	return r.Status == StatusUsed
}

func (r *RedeemCode) IsExpired() bool {
	return r.IsExpiredAt(time.Now())
}

func (r *RedeemCode) IsExpiredAt(now time.Time) bool {
	if r == nil {
		return false
	}
	if r.Status == StatusExpired {
		return true
	}
	return r.Status == StatusUnused && r.ExpiresAt != nil && !r.ExpiresAt.After(now)
}

func (r *RedeemCode) CanUse() bool {
	return r.Status == StatusUnused && !r.IsExpired()
}

func GenerateRedeemCode() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func GeneratePrefixedRedeemCode(prefix string) (string, error) {
	prefix = strings.ToUpper(strings.Trim(strings.TrimSpace(prefix), "-"))
	if prefix == "" {
		return GenerateRedeemCode()
	}

	availableRandomChars := 32 - len(prefix) - 1
	if availableRandomChars <= 0 {
		return "", fmt.Errorf("redeem code prefix too long")
	}

	byteCount := (availableRandomChars + 1) / 2
	b := make([]byte, byteCount)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	randomPart := strings.ToUpper(hex.EncodeToString(b))
	if len(randomPart) > availableRandomChars {
		randomPart = randomPart[:availableRandomChars]
	}
	return prefix + "-" + randomPart, nil
}

func redeemCodePrefixForGenerate(codeType, notes string) string {
	if codeType == RedeemTypeBalance {
		if _, ok := parseLDCCodeMetadata(notes); ok {
			return "LDC"
		}
		return "GEN"
	}

	switch codeType {
	case RedeemTypeConcurrency:
		return "CON"
	case RedeemTypeSubscription:
		return "SUB"
	case RedeemTypeInvitation:
		return "INV"
	default:
		return "GEN"
	}
}
