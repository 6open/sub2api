package handler

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	openWebUIHandoffTTL          = 60 * time.Second
	openWebUIHandoffDomain       = "open-webui-handoff."
	openWebUIHandoffConsumedKey  = "auth:open-webui-handoff:consumed:"
	openWebUIHandoffSecretHeader = "X-Open-WebUI-SSO-Secret"
)

type openWebUIHandoffPayload struct {
	UserID    int64  `json:"user_id"`
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"exp"`
}

type openWebUIHandoffStartResponse struct {
	URL string `json:"url"`
}

type openWebUIHandoffVerifyRequest struct {
	Token string `json:"token" binding:"required"`
}

type openWebUIHandoffIdentity struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// StartOpenWebUIHandoff creates a short-lived URL that transfers the current
// sub2api identity without exposing its access token or API keys.
func (h *AuthHandler) StartOpenWebUIHandoff(c *gin.Context) {
	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !user.IsActive() {
		response.ErrorFrom(c, infraerrors.Forbidden("OPEN_WEBUI_SSO_USER_INACTIVE", "user is not active"))
		return
	}

	nonce, err := newOpenWebUIHandoffNonce()
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OPEN_WEBUI_SSO_NONCE_FAILED", "failed to create handoff nonce").WithCause(err))
		return
	}
	token, err := h.signOpenWebUIHandoff(openWebUIHandoffPayload{
		UserID:    user.ID,
		Nonce:     nonce,
		ExpiresAt: time.Now().Add(openWebUIHandoffTTL).Unix(),
	})
	if err != nil {
		response.ErrorFrom(c, infraerrors.InternalServer("OPEN_WEBUI_SSO_SIGN_FAILED", "failed to create handoff token").WithCause(err))
		return
	}

	handoffURL, err := buildOpenWebUIHandoffURL(os.Getenv("OPEN_WEBUI_SSO_URL"), token)
	if err != nil {
		response.ErrorFrom(c, infraerrors.ServiceUnavailable("OPEN_WEBUI_SSO_NOT_CONFIGURED", "Open WebUI SSO is not configured").WithCause(err))
		return
	}

	response.Success(c, openWebUIHandoffStartResponse{URL: handoffURL})
}

// VerifyOpenWebUIHandoff validates and atomically consumes a handoff. The
// endpoint is called server-to-server by the Open WebUI auth bridge.
func (h *AuthHandler) VerifyOpenWebUIHandoff(redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !validOpenWebUIVerifySecret(c.GetHeader(openWebUIHandoffSecretHeader), os.Getenv("OPEN_WEBUI_SSO_VERIFY_SECRET")) {
			response.ErrorFrom(c, infraerrors.Unauthorized("OPEN_WEBUI_SSO_UNAUTHORIZED", "invalid SSO verifier credentials"))
			return
		}

		var req openWebUIHandoffVerifyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.BadRequest(c, "Invalid request: "+err.Error())
			return
		}

		payload, ok := h.verifyOpenWebUIHandoff(req.Token)
		if !ok {
			response.ErrorFrom(c, infraerrors.Unauthorized("OPEN_WEBUI_SSO_INVALID", "invalid or expired handoff token"))
			return
		}

		consumed, err := consumeOpenWebUIHandoff(c.Request.Context(), redisClient, payload)
		if err != nil {
			response.ErrorFrom(c, infraerrors.ServiceUnavailable("OPEN_WEBUI_SSO_STORE_UNAVAILABLE", "handoff store is unavailable").WithCause(err))
			return
		}
		if !consumed {
			response.ErrorFrom(c, infraerrors.Unauthorized("OPEN_WEBUI_SSO_ALREADY_USED", "handoff token has already been used"))
			return
		}

		user, err := h.userService.GetByID(c.Request.Context(), payload.UserID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if !user.IsActive() {
			response.ErrorFrom(c, infraerrors.Forbidden("OPEN_WEBUI_SSO_USER_INACTIVE", "user is not active"))
			return
		}

		response.Success(c, openWebUIHandoffIdentity{
			UserID:   user.ID,
			Email:    user.Email,
			Username: user.Username,
			Role:     user.Role,
		})
	}
}

func newOpenWebUIHandoffNonce() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func buildOpenWebUIHandoffURL(rawURL, token string) (string, error) {
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if target.Scheme != "https" || target.Host == "" || strings.TrimSpace(token) == "" {
		return "", errors.New("invalid Open WebUI SSO URL or token")
	}
	query := target.Query()
	query.Set("handoff", token)
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (h *AuthHandler) signOpenWebUIHandoff(payload openWebUIHandoffPayload) (string, error) {
	secret := h.oauthBindCookieSecret()
	if secret == "" {
		return "", errors.New("missing handoff signing secret")
	}
	if payload.UserID <= 0 || payload.Nonce == "" || payload.ExpiresAt <= 0 {
		return "", errors.New("invalid handoff payload")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(openWebUIHandoffDomain + body))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

func (h *AuthHandler) verifyOpenWebUIHandoff(value string) (*openWebUIHandoffPayload, bool) {
	secret := h.oauthBindCookieSecret()
	body, signature, ok := strings.Cut(strings.TrimSpace(value), ".")
	if secret == "" || !ok || body == "" || signature == "" {
		return nil, false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(openWebUIHandoffDomain + body))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, false
	}
	var payload openWebUIHandoffPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false
	}
	if payload.UserID <= 0 || payload.Nonce == "" || payload.ExpiresAt <= time.Now().Unix() {
		return nil, false
	}
	return &payload, true
}

func validOpenWebUIVerifySecret(provided, expected string) bool {
	provided = strings.TrimSpace(provided)
	expected = strings.TrimSpace(expected)
	return provided != "" && expected != "" && hmac.Equal([]byte(provided), []byte(expected))
}

func consumeOpenWebUIHandoff(ctx context.Context, redisClient *redis.Client, payload *openWebUIHandoffPayload) (bool, error) {
	if redisClient == nil || payload == nil || payload.Nonce == "" {
		return false, errors.New("invalid handoff store input")
	}
	ttl := time.Until(time.Unix(payload.ExpiresAt, 0))
	if ttl <= 0 {
		return false, nil
	}
	return redisClient.SetNX(ctx, openWebUIHandoffConsumedKey+payload.Nonce, "1", ttl).Result()
}
