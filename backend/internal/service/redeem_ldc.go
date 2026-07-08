package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/authidentity"
	"github.com/Wei-Shaw/sub2api/ent/redeemcode"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	ldcCodeKind      = "ldc"
	ldcPromoUSDLimit = 10.0
	ldcPromoRate     = 10.0
	ldcNormalRate    = 50.0
)

type ldcCodeMetadata struct {
	CodeKind                string  `json:"code_kind,omitempty"`
	Source                  string  `json:"source,omitempty"`
	LinuxDoUserSub          string  `json:"linuxdo_user_sub,omitempty"`
	LinuxDoUsername         string  `json:"linuxdo_username,omitempty"`
	USDValue                float64 `json:"usd_value,omitempty"`
	RedeemedLinuxDoSubject  string  `json:"redeemed_linuxdo_subject,omitempty"`
	RedeemedLinuxDoUsername string  `json:"redeemed_linuxdo_username,omitempty"`
	RedeemedUserID          int64   `json:"redeemed_user_id,omitempty"`
	LDCAmount               float64 `json:"ldc_amount,omitempty"`
	CreditedUSD             float64 `json:"credited_usd,omitempty"`
}

type linuxDoRedeemIdentity struct {
	Subject  string
	Username string
}

func calculateLDCCodeCreditUSD(ldcAmount, issuedUSD float64) float64 {
	if ldcAmount <= 0 {
		return 0
	}
	if issuedUSD < 0 {
		issuedUSD = 0
	}

	remainingPromoUSD := ldcPromoUSDLimit - issuedUSD
	if remainingPromoUSD < 0 {
		remainingPromoUSD = 0
	}

	promoLDC := math.Min(ldcAmount, remainingPromoUSD*ldcPromoRate)
	normalLDC := ldcAmount - promoLDC
	return promoLDC/ldcPromoRate + normalLDC/ldcNormalRate
}

func parseLDCCodeMetadata(notes string) (ldcCodeMetadata, bool) {
	var meta ldcCodeMetadata
	if strings.TrimSpace(notes) == "" {
		return meta, false
	}
	if err := json.Unmarshal([]byte(notes), &meta); err != nil {
		return ldcCodeMetadata{}, false
	}
	meta.CodeKind = strings.ToLower(strings.TrimSpace(meta.CodeKind))
	if meta.CodeKind != ldcCodeKind {
		return meta, false
	}
	return meta, true
}

func mergeLDCCodeRedemptionNotes(notes string, userID int64, ldcAmount, creditedUSD float64) string {
	merged := map[string]any{}
	if strings.TrimSpace(notes) != "" {
		_ = json.Unmarshal([]byte(notes), &merged)
	}
	merged["code_kind"] = ldcCodeKind
	merged["redeemed_user_id"] = userID
	merged["ldc_amount"] = ldcAmount
	merged["credited_usd"] = creditedUSD

	encoded, err := json.Marshal(merged)
	if err != nil {
		return notes
	}
	return string(encoded)
}

func (s *RedeemService) validateLDCUserLinuxDoBinding(ctx context.Context, client *dbent.Client, userID int64, meta ldcCodeMetadata) error {
	if client == nil {
		return infraerrors.ServiceUnavailable("LDC_REDEEM_AUTH_IDENTITY_UNAVAILABLE", "auth identity service is unavailable")
	}

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(userID),
			authidentity.ProviderTypeEQ("linuxdo"),
			authidentity.ProviderKeyEQ("linuxdo"),
		).
		First(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return infraerrors.BadRequest("LDC_REDEEM_LINUXDO_BIND_REQUIRED", "please bind your LinuxDO account before redeeming LDC codes")
		}
		return infraerrors.InternalServer("LDC_REDEEM_AUTH_IDENTITY_LOOKUP_FAILED", "failed to inspect LinuxDO binding").WithCause(err)
	}

	expectedSubject := strings.TrimSpace(meta.LinuxDoUserSub)
	if expectedSubject != "" && strings.TrimSpace(identity.ProviderSubject) != expectedSubject {
		return infraerrors.BadRequest("LDC_REDEEM_LINUXDO_SUBJECT_MISMATCH", "this LDC redeem code can only be used by the LinuxDO account that purchased it")
	}
	return nil
}

func (s *RedeemService) issuedLDCUSDForUser(ctx context.Context, client *dbent.Client, userID int64) (float64, error) {
	codes, err := client.RedeemCode.Query().
		Where(
			redeemcode.StatusEQ(StatusUsed),
			redeemcode.UsedByEQ(userID),
			redeemcode.TypeIn(RedeemTypeBalance, AdjustmentTypeAdminBalance),
			redeemcode.NotesContains(`"code_kind":"ldc"`),
		).
		All(ctx)
	if err != nil {
		return 0, err
	}

	total := 0.0
	for _, code := range codes {
		notes := ""
		if code.Notes != nil {
			notes = *code.Notes
		}
		total += issuedUSDFromLDCNotes(notes)
	}
	return total, nil
}

func issuedUSDFromLDCNotes(notes string) float64 {
	meta, ok := parseLDCCodeMetadata(notes)
	if !ok || meta.CreditedUSD <= 0 {
		return 0
	}
	return meta.CreditedUSD
}
