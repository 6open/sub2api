package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func signOpenWebUIUserJWT(t *testing.T, secret []byte, claims openWebUIUserClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	require.NoError(t, err)
	return signed
}

func TestParseOpenWebUIUserJWT(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	raw := signOpenWebUIUserJWT(t, secret, openWebUIUserClaims{
		Email: "person@example.com",
		Role:  "user",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "open-webui-user-id",
			Issuer:    "open-webui",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
		},
	})

	claims, err := parseOpenWebUIUserJWT(raw, secret, now)
	require.NoError(t, err)
	require.Equal(t, "person@example.com", claims.Email)
	require.Equal(t, "open-webui-user-id", claims.Subject)
}

func TestParseOpenWebUIUserJWTRejectsInvalidIdentity(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	tests := []struct {
		name   string
		issuer string
		email  string
		expiry time.Time
		signAs []byte
	}{
		{name: "forged signature", issuer: "open-webui", email: "person@example.com", expiry: now.Add(time.Minute), signAs: []byte("abcdef0123456789abcdef0123456789")},
		{name: "expired", issuer: "open-webui", email: "person@example.com", expiry: now.Add(-time.Minute), signAs: secret},
		{name: "wrong issuer", issuer: "another-service", email: "person@example.com", expiry: now.Add(time.Minute), signAs: secret},
		{name: "missing email", issuer: "open-webui", expiry: now.Add(time.Minute), signAs: secret},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := signOpenWebUIUserJWT(t, tt.signAs, openWebUIUserClaims{
				Email: tt.email,
				RegisteredClaims: jwt.RegisteredClaims{
					Subject:   "user-id",
					Issuer:    tt.issuer,
					IssuedAt:  jwt.NewNumericDate(now),
					ExpiresAt: jwt.NewNumericDate(tt.expiry),
				},
			})
			_, err := parseOpenWebUIUserJWT(raw, secret, now)
			require.Error(t, err)
		})
	}
}

func TestLoadOpenWebUIDelegationConfigFailsClosed(t *testing.T) {
	t.Setenv("OPEN_WEBUI_SERVICE_API_KEY_ID", "69")
	t.Setenv("OPEN_WEBUI_DEFAULT_GROUP_ID", "12")
	t.Setenv("OPEN_WEBUI_FORWARD_USER_JWT_SECRET", "short")

	cfg := loadOpenWebUIDelegationConfig()
	require.Equal(t, int64(69), cfg.serviceAPIKeyID)
	require.Error(t, cfg.err)
}

func TestLoadOpenWebUIDelegationConfigUsesDedicatedImageGroup(t *testing.T) {
	t.Setenv("OPEN_WEBUI_SERVICE_API_KEY_ID", "69")
	t.Setenv("OPEN_WEBUI_DEFAULT_GROUP_ID", "2")
	t.Setenv("OPEN_WEBUI_IMAGE_GROUP_ID", "7")
	t.Setenv("OPEN_WEBUI_FORWARD_USER_JWT_SECRET", "0123456789abcdef0123456789abcdef")

	cfg := loadOpenWebUIDelegationConfig()
	require.NoError(t, cfg.err)
	require.Equal(t, int64(2), cfg.defaultGroupID)
	require.Equal(t, int64(7), cfg.imageGroupID)
}

func TestIsOpenWebUIImagePath(t *testing.T) {
	require.True(t, isOpenWebUIImagePath("/v1/images/generations"))
	require.True(t, isOpenWebUIImagePath("/v1/images/edits"))
	require.False(t, isOpenWebUIImagePath("/v1/chat/completions"))
}

func TestOpenWebUIServiceKeyRequiresSignedUserIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/v1/models", nil)
	cfg := openWebUIDelegationConfig{
		serviceAPIKeyID: 69,
		defaultGroupID:  12,
		jwtSecret:       []byte("0123456789abcdef0123456789abcdef"),
	}

	resolved, ok := resolveOpenWebUIDelegation(ctx, nil, cfg, &service.APIKey{ID: 69})
	require.False(t, ok)
	require.Nil(t, resolved)
	require.Equal(t, 401, recorder.Code)
}
