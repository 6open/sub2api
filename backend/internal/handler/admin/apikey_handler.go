package admin

import (
	"context"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminAPIKeyHandler handles admin API key management
type AdminAPIKeyHandler struct {
	adminService  service.AdminService
	apiKeyCreator userAPIKeyCreator
}

type userAPIKeyCreator interface {
	Create(ctx context.Context, userID int64, req service.CreateAPIKeyRequest) (*service.APIKey, error)
}

// NewAdminAPIKeyHandler creates a new admin API key handler
func NewAdminAPIKeyHandler(adminService service.AdminService, apiKeyService *service.APIKeyService) *AdminAPIKeyHandler {
	return &AdminAPIKeyHandler{
		adminService:  adminService,
		apiKeyCreator: apiKeyService,
	}
}

// AdminCreateUserAPIKeyRequest represents an administrator-created user API key.
type AdminCreateUserAPIKeyRequest struct {
	Name          string   `json:"name" binding:"required"`
	GroupID       *int64   `json:"group_id"`
	IPWhitelist   []string `json:"ip_whitelist"`
	IPBlacklist   []string `json:"ip_blacklist"`
	Quota         *float64 `json:"quota" binding:"omitempty,gte=0"`
	ExpiresInDays *int     `json:"expires_in_days" binding:"omitempty,gte=1"`
	RateLimit5h   *float64 `json:"rate_limit_5h" binding:"omitempty,gte=0"`
	RateLimit1d   *float64 `json:"rate_limit_1d" binding:"omitempty,gte=0"`
	RateLimit7d   *float64 `json:"rate_limit_7d" binding:"omitempty,gte=0"`
}

// AdminUpdateAPIKeyGroupRequest represents the request to update an API key.
type AdminUpdateAPIKeyGroupRequest struct {
	GroupID             *int64 `json:"group_id"`               // nil=不修改, 0=解绑, >0=绑定到目标分组
	ResetRateLimitUsage *bool  `json:"reset_rate_limit_usage"` // true=重置 5h/1d/7d 限速用量
}

// CreateForUser creates an API key owned by the selected user.
// POST /api/v1/admin/users/:id/api-keys
func (h *AdminAPIKeyHandler) CreateForUser(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || userID <= 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req AdminCreateUserAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		response.BadRequest(c, "Invalid request: name is required")
		return
	}
	if h.apiKeyCreator == nil {
		response.InternalError(c, "API key service is unavailable")
		return
	}

	input := service.CreateAPIKeyRequest{
		Name:              req.Name,
		GroupID:           req.GroupID,
		IPWhitelist:       req.IPWhitelist,
		IPBlacklist:       req.IPBlacklist,
		ExpiresInDays:     req.ExpiresInDays,
		AdminManagedQuota: true,
	}
	if req.Quota != nil {
		input.Quota = *req.Quota
	}
	if req.RateLimit5h != nil {
		input.RateLimit5h = *req.RateLimit5h
	}
	if req.RateLimit1d != nil {
		input.RateLimit1d = *req.RateLimit1d
	}
	if req.RateLimit7d != nil {
		input.RateLimit7d = *req.RateLimit7d
	}

	idempotencyPayload := struct {
		UserID int64                        `json:"user_id"`
		Body   AdminCreateUserAPIKeyRequest `json:"body"`
	}{UserID: userID, Body: req}
	executeAdminIdempotentJSON(c, "admin.users.api_keys.create", idempotencyPayload, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		key, createErr := h.apiKeyCreator.Create(ctx, userID, input)
		if createErr != nil {
			return nil, createErr
		}
		return dto.APIKeyFromService(key), nil
	})
}

// UpdateGroup handles updating an API key's admin-managed fields.
// PUT /api/v1/admin/api-keys/:id
func (h *AdminAPIKeyHandler) UpdateGroup(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	var req AdminUpdateAPIKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var resetKey *service.APIKey
	if req.ResetRateLimitUsage != nil && *req.ResetRateLimitUsage {
		resetKey, err = h.adminService.AdminResetAPIKeyRateLimitUsage(c.Request.Context(), keyID)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	result, err := h.adminService.AdminUpdateAPIKeyGroupID(c.Request.Context(), keyID, req.GroupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if resetKey != nil && req.GroupID == nil {
		result.APIKey = resetKey
	}

	resp := struct {
		APIKey                 *dto.APIKey `json:"api_key"`
		AutoGrantedGroupAccess bool        `json:"auto_granted_group_access"`
		GrantedGroupID         *int64      `json:"granted_group_id,omitempty"`
		GrantedGroupName       string      `json:"granted_group_name,omitempty"`
	}{
		APIKey:                 dto.APIKeyFromService(result.APIKey),
		AutoGrantedGroupAccess: result.AutoGrantedGroupAccess,
		GrantedGroupID:         result.GrantedGroupID,
		GrantedGroupName:       result.GrantedGroupName,
	}
	response.Success(c, resp)
}
