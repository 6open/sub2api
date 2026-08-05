package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const (
	openWebUIUserJWTHeader   = "X-OpenWebUI-User-Jwt"
	maxOpenWebUIUserJWTBytes = 8192
)

type openWebUIDelegationConfig struct {
	serviceAPIKeyID int64
	defaultGroupID  int64
	imageGroupID    int64
	jwtSecret       []byte
	err             error
}

type openWebUIUserClaims struct {
	Email string `json:"email"`
	Role  string `json:"role"`
	jwt.RegisteredClaims
}

func loadOpenWebUIDelegationConfig() openWebUIDelegationConfig {
	rawServiceKeyID := strings.TrimSpace(os.Getenv("OPEN_WEBUI_SERVICE_API_KEY_ID"))
	if rawServiceKeyID == "" {
		return openWebUIDelegationConfig{}
	}
	serviceKeyID, err := strconv.ParseInt(rawServiceKeyID, 10, 64)
	if err != nil || serviceKeyID <= 0 {
		return openWebUIDelegationConfig{err: fmt.Errorf("invalid OPEN_WEBUI_SERVICE_API_KEY_ID")}
	}
	rawGroupID := strings.TrimSpace(os.Getenv("OPEN_WEBUI_DEFAULT_GROUP_ID"))
	groupID, err := strconv.ParseInt(rawGroupID, 10, 64)
	if err != nil || groupID <= 0 {
		return openWebUIDelegationConfig{serviceAPIKeyID: serviceKeyID, err: fmt.Errorf("invalid OPEN_WEBUI_DEFAULT_GROUP_ID")}
	}
	imageGroupID := groupID
	if rawImageGroupID := strings.TrimSpace(os.Getenv("OPEN_WEBUI_IMAGE_GROUP_ID")); rawImageGroupID != "" {
		imageGroupID, err = strconv.ParseInt(rawImageGroupID, 10, 64)
		if err != nil || imageGroupID <= 0 {
			return openWebUIDelegationConfig{serviceAPIKeyID: serviceKeyID, defaultGroupID: groupID, err: fmt.Errorf("invalid OPEN_WEBUI_IMAGE_GROUP_ID")}
		}
	}
	secret := []byte(strings.TrimSpace(os.Getenv("OPEN_WEBUI_FORWARD_USER_JWT_SECRET")))
	if len(secret) < 32 {
		return openWebUIDelegationConfig{serviceAPIKeyID: serviceKeyID, err: fmt.Errorf("OPEN_WEBUI_FORWARD_USER_JWT_SECRET must be at least 32 bytes")}
	}
	return openWebUIDelegationConfig{
		serviceAPIKeyID: serviceKeyID,
		defaultGroupID:  groupID,
		imageGroupID:    imageGroupID,
		jwtSecret:       secret,
	}
}

func parseOpenWebUIUserJWT(raw string, secret []byte, now time.Time) (*openWebUIUserClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxOpenWebUIUserJWTBytes {
		return nil, errors.New("missing or oversized Open WebUI user JWT")
	}
	claims := &openWebUIUserClaims{}
	token, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
		jwt.WithIssuer("open-webui"),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time { return now }),
		jwt.WithLeeway(5*time.Second),
	)
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid Open WebUI user JWT: %w", err)
	}
	if strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.Email) == "" {
		return nil, errors.New("open WebUI user JWT is missing required claims")
	}
	return claims, nil
}

func resolveOpenWebUIDelegation(c *gin.Context, apiKeyService *service.APIKeyService, cfg openWebUIDelegationConfig, apiKey *service.APIKey) (*service.APIKey, bool) {
	if cfg.serviceAPIKeyID == 0 || apiKey == nil || apiKey.ID != cfg.serviceAPIKeyID {
		return apiKey, true
	}
	if cfg.err != nil {
		AbortWithError(c, http.StatusServiceUnavailable, "OPEN_WEBUI_DELEGATION_UNAVAILABLE", "Open WebUI user billing is not configured")
		return nil, false
	}
	claims, err := parseOpenWebUIUserJWT(c.GetHeader(openWebUIUserJWTHeader), cfg.jwtSecret, time.Now())
	if err != nil {
		AbortWithError(c, http.StatusUnauthorized, "INVALID_OPEN_WEBUI_USER", "Invalid Open WebUI user identity")
		return nil, false
	}
	var delegated *service.APIKey
	if isOpenWebUIImagePath(c.Request.URL.Path) {
		delegated, err = apiKeyService.ResolveOpenWebUIImageAPIKey(c.Request.Context(), claims.Email, cfg.imageGroupID)
	} else {
		delegated, err = apiKeyService.ResolveOpenWebUIAPIKey(c.Request.Context(), claims.Email, cfg.defaultGroupID)
	}
	if err != nil {
		AbortWithError(c, http.StatusServiceUnavailable, "OPEN_WEBUI_USER_KEY_UNAVAILABLE", "Unable to resolve the Open WebUI user's API key")
		return nil, false
	}
	return delegated, true
}

func isOpenWebUIImagePath(path string) bool {
	return strings.HasPrefix(path, "/v1/images/")
}
