package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/edgebridge"
	"github.com/Wei-Shaw/sub2api/internal/pkg/edgepermit"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type edgeSnapshot struct {
	Key         service.APIKey
	Account     service.Account
	Model       string
	Rate        float64
	PricingAt   time.Time
	UserAgent   string
	IP          string
	PayloadHash string
	Effort      *string
	Tier        *string
	Fields      service.ChannelUsageFields
	Stream      bool
	CyberKey    string
}
type edgeLease struct {
	ID            string
	State         string
	CreatedAt     int64
	Deadline      int64
	LastBeat      int64
	Snapshot      *edgeSnapshot
	UpstreamModel string
	Receipt       *edgebridge.Receipt
	ReceiptHash   string
	Bill          *service.UsageBillingCommand
	Log           *service.UsageLog
	SlotAccounts  []int64
	Observed      bool
}
type EdgeControl struct {
	mu     sync.Mutex
	h      *OpenAIGatewayHandler
	store  *edgebridge.Store
	token  string
	key    ed25519.PrivateKey
	slots  chan struct{}
	active map[string]bool
}

func edgeExecutionURLAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Port() != "" || u.User != nil || u.RawQuery != "" {
		return false
	}
	if u.Host == "chatgpt.com" && u.Path == "/backend-api/codex/responses" {
		return true
	}
	return (u.Host == "cli-chat-proxy.grok.com" || u.Host == "api.x.ai") && (u.Path == "/v1/responses" || u.Path == "/responses")
}

func (h *OpenAIGatewayHandler) edgeSnapshot(c *gin.Context, k *service.APIKey, a *service.Account, model string, at time.Time, fields service.ChannelUsageFields) *edgeSnapshot {
	g := k.Group
	group := &service.Group{ID: g.ID, Platform: g.Platform, SubscriptionType: g.SubscriptionType, RateMultiplier: g.RateMultiplier,
		PeakRateEnabled: g.PeakRateEnabled, PeakStart: g.PeakStart, PeakEnd: g.PeakEnd, PeakRateMultiplier: g.PeakRateMultiplier,
		LongContextPricingEnabled: g.LongContextPricingEnabled, ModelPricing: g.ModelPricing}
	key := service.APIKey{ID: k.ID, UserID: k.UserID, GroupID: k.GroupID, Group: group, User: &service.User{ID: k.User.ID, Role: k.User.Role, Concurrency: k.User.Concurrency}, Status: k.Status}
	account := edgeBillingAccount(a)
	return &edgeSnapshot{Key: key, Account: account, Model: model, PricingAt: at, Fields: fields,
		Rate: h.gatewayService.ResolveUserGroupRateMultiplier(c.Request.Context(), k.UserID, g.ID, g.RateMultiplier), UserAgent: c.GetHeader("User-Agent"), IP: ip.GetClientIP(c)}
}

func edgeBillingAccount(a *service.Account) service.Account {
	return service.Account{ID: a.ID, Platform: a.Platform, Type: a.Type, RateMultiplier: a.RateMultiplier, Concurrency: a.Concurrency,
		Extra: map[string]any{"openai_long_context_billing_enabled": a.IsOpenAILongContextBillingEnabled()}}
}

func NewEdgeControl(h *OpenAIGatewayHandler) (*EdgeControl, error) {
	path := os.Getenv("LKLB_EDGE_CONTROL_CONFIG")
	if path == "" {
		path = "/app/data/lklb-edge/control.json"
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Token    string `json:"token"`
		KeyFile  string `json:"key_file"`
		StateDir string `json:"state_dir"`
	}
	if json.Unmarshal(raw, &cfg) != nil || len(cfg.Token) < 32 {
		return nil, errors.New("invalid edge control config")
	}
	keyData, err := os.ReadFile(cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(keyData)
	if block == nil {
		return nil, errors.New("invalid signing key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("Ed25519 key required")
	}
	store, err := edgebridge.OpenStore(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	e := &EdgeControl{h: h, store: store, token: cfg.Token, key: key, slots: make(chan struct{}, edgebridge.MaxConcurrent), active: make(map[string]bool)}
	ids, err := store.IDs()
	if err != nil {
		store.Close()
		return nil, err
	}
	for _, id := range ids {
		var l edgeLease
		if err := store.Load(id, &l); err != nil {
			store.Close()
			return nil, err
		}
		if l.State == "preparing" || l.State == "authorized" || l.State == "pending" {
			e.active[id] = true
		}
	}
	go e.recoverLoop()
	return e, nil
}
func (e *EdgeControl) Guard(c *gin.Context) {
	token := c.GetHeader(edgebridge.NodeHeader)
	if len(token) != len(e.token) || subtle.ConstantTimeCompare([]byte(token), []byte(e.token)) != 1 {
		c.AbortWithStatus(404)
		return
	}
	if clientIP := c.GetHeader("X-Lklb-Client-IP"); clientIP != "" {
		if net.ParseIP(clientIP) == nil {
			c.AbortWithStatus(400)
			return
		}
		c.Request.RemoteAddr = net.JoinHostPort(clientIP, "0")
		for _, h := range []string{"X-Real-IP", "X-Forwarded-For", "Forwarded", "CF-Connecting-IP", "True-Client-IP"} {
			c.Request.Header.Del(h)
		}
	}
	c.Next()
}
func (e *EdgeControl) Pilot(c *gin.Context) {
	k, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || k.User == nil || !edgebridge.PilotUserAllowed(k.User.ID) || k.UserID != k.User.ID || k.Group == nil || (k.Group.Platform != service.PlatformOpenAI && k.Group.Platform != service.PlatformGrok) || k.Group.IsSubscriptionType() || k.Quota != 0 || k.HasRateLimits() {
		c.AbortWithStatusJSON(403, gin.H{"error": "pilot requires an approved user and unlimited OpenAI/Grok key"})
		return
	}
	c.Next()
}
func (e *EdgeControl) Prepare(c *gin.Context) {
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		c.JSON(503, gin.H{"error": "edge busy"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, edgebridge.MaxBody+1))
	if err != nil || len(data) > edgebridge.MaxBody {
		c.JSON(413, gin.H{"error": "request too large"})
		return
	}
	model := gjson.GetBytes(data, "model").String()
	if !edgebridge.SupportedInput(data) {
		c.JSON(400, gin.H{"error": "pilot supports text and image inputs, not audio, files or server-side tools"})
		return
	}
	if model == "" {
		c.JSON(400, gin.H{"error": "model is required"})
		return
	}
	// Reject media/server-side tools; function tools are executed by the client.
	if !edgebridge.ClientToolsOnly(data) {
		c.JSON(400, gin.H{"error": "pilot supports client-executed tools only"})
		return
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		c.Status(503)
		return
	}
	id := hex.EncodeToString(nonce)
	k, _ := middleware.GetAPIKeyFromContext(c)
	e.mu.Lock()
	if len(e.active) >= edgebridge.MaxConcurrent {
		e.mu.Unlock()
		c.JSON(503, gin.H{"error": "edge active request limit"})
		return
	}
	initial := edgeLease{ID: id, State: "preparing", CreatedAt: time.Now().Unix(), LastBeat: time.Now().Unix(), Deadline: time.Now().Add(time.Minute).Unix(), Snapshot: &edgeSnapshot{Key: service.APIKey{ID: k.ID, UserID: k.UserID}}}
	if err := e.store.Save(id, &initial, true); err != nil {
		e.mu.Unlock()
		c.Status(503)
		return
	}
	e.active[id] = true
	e.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Second)
	defer cancel()
	session := &edgebridge.Session{ID: id, UserID: k.UserID, KeyID: k.ID, UserMax: k.User.Concurrency}
	defer func() {
		if !session.Issued.Load() {
			e.mu.Lock()
			defer e.mu.Unlock()
			var l edgeLease
			if e.store.Load(id, &l) == nil {
				l.State = "expired"
				_ = e.store.Save(id, &l, false)
			}
			delete(e.active, id)
		}
	}()
	session.BeforeAccountSlot = func(accountID int64) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		var l edgeLease
		if err := e.store.Load(id, &l); err != nil {
			return err
		}
		if l.State != "preparing" || time.Now().Unix() > l.Deadline {
			return errors.New("authorization expired")
		}
		for _, a := range l.SlotAccounts {
			if a == accountID {
				return nil
			}
		}
		l.SlotAccounts = append(l.SlotAccounts, accountID)
		return e.store.Save(id, &l, false)
	}
	var plan *edgebridge.Plan
	var captureErr error
	session.Capture = func(req *http.Request, accountID int64) error {
		captureErr = func() error {
			snap, ok := session.Snapshot.(*edgeSnapshot)
			if !ok || snap.Account.ID != accountID {
				return errors.New("missing authorized snapshot")
			}
			if req.Method != "POST" || !edgeExecutionURLAllowed(req.URL.String()) {
				return errors.New("upstream not enabled")
			}
			body, err := io.ReadAll(io.LimitReader(req.Body, edgebridge.MaxBody+1))
			if err != nil || len(body) > edgebridge.MaxBody {
				return errors.New("invalid upstream body")
			}
			headers := req.Header.Clone()
			for _, k := range []string{"Connection", "Transfer-Encoding", "Content-Length", "Host", edgebridge.NodeHeader} {
				headers.Del(k)
			}
			headers.Set("Accept-Encoding", "identity")
			now := time.Now()
			upModel := gjson.GetBytes(body, "model").String()
			snap.PayloadHash = edgepermit.BodyHash(data)
			snap.CyberKey = service.CyberSessionBlockKey(snap.Key.ID, c, data)
			snap.Stream = gjson.GetBytes(data, "stream").Bool()
			if effort := gjson.GetBytes(body, "reasoning.effort").String(); effort != "" {
				snap.Effort = &effort
			}
			if tier := gjson.GetBytes(body, "service_tier").String(); tier != "" {
				snap.Tier = &tier
			}
			claims := edgepermit.Claims{Version: 1, RequestID: id, NodeID: "bwg", UserID: snap.Key.UserID, APIKeyID: snap.Key.ID, AccountID: accountID, Model: upModel, BodySHA256: edgepermit.BodyHash(body), RequestSHA256: edgepermit.RequestHash(req.URL.String(), headers, body), IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix()}
			permit, err := edgepermit.Sign(e.key, claims)
			if err != nil {
				return err
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			var lease edgeLease
			if err := e.store.Load(id, &lease); err != nil {
				return err
			}
			if lease.State != "preparing" || now.Unix() > lease.Deadline {
				return errors.New("authorization expired")
			}
			lease.State = "authorized"
			lease.LastBeat = now.Unix()
			lease.Deadline = now.Add(time.Duration(edgebridge.ExecutionTimeoutSeconds+60) * time.Second).Unix()
			lease.Snapshot = snap
			lease.UpstreamModel = upModel
			if err := e.store.Save(id, &lease, false); err != nil {
				return err
			}
			session.Issued.Store(true)
			plan = &edgebridge.Plan{ID: id, Permit: permit, URL: req.URL.String(), Headers: headers, Body: body, Model: upModel, ExpiresAt: claims.ExpiresAt}
			return nil
		}()
		return captureErr
	}
	c.Request = c.Request.WithContext(edgebridge.WithSession(ctx, session))
	c.Request.Body = io.NopCloser(strings.NewReader(string(data)))
	c.Request.URL.Path = "/v1/responses"
	c.Request.Header.Del(edgebridge.NodeHeader)
	originalWriter := c.Writer
	captured := newEdgeCaptureWriter()
	c.Writer = captured
	func() { defer func() { c.Writer = originalWriter }(); e.h.Responses(c) }()
	if plan != nil && !c.Writer.Written() {
		c.JSON(200, plan)
		return
	}
	if captured.Status() >= 400 {
		c.Data(captured.Status(), "application/json", captured.rec.Body.Bytes())
		return
	}
	if !c.Writer.Written() {
		c.JSON(503, gin.H{"error": "edge authorization unavailable"})
	}
}
func (e *EdgeControl) release(ctx context.Context, l *edgeLease) error {
	s := l.Snapshot
	accounts := l.SlotAccounts
	if len(accounts) == 0 {
		accounts = []int64{0}
	}
	var result error
	for _, id := range accounts {
		result = errors.Join(result, e.h.concurrencyHelper.concurrencyService.ReleaseEdgeSlots(ctx, l.ID, s.Key.UserID, s.Key.ID, id))
	}
	if result == nil {
		delete(e.active, l.ID)
	}
	return result
}
func (e *EdgeControl) Heartbeat(c *gin.Context) {
	var in struct {
		ID                string `json:"id"`
		TurnStateReceived bool   `json:"turn_state_received"`
	}
	if c.ShouldBindJSON(&in) != nil {
		c.Status(400)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var l edgeLease
	if e.store.Load(in.ID, &l) != nil {
		c.Status(404)
		return
	}
	if l.State != "authorized" || time.Now().Unix() > l.Deadline || time.Now().Unix()-l.LastBeat > 45 {
		c.Status(409)
		return
	}
	k, err := e.h.apiKeyService.GetByID(c.Request.Context(), l.Snapshot.Key.ID)
	if err != nil || !k.IsActive() || k.IsExpired() || k.User == nil || !k.User.IsActive() || !edgebridge.PilotUserAllowed(k.User.ID) || k.User.ID != l.Snapshot.Key.UserID {
		c.Status(403)
		return
	}
	s := l.Snapshot
	if e.h.concurrencyHelper.concurrencyService.RefreshEdgeSlots(c.Request.Context(), l.ID, s.Key.UserID, s.Key.ID, s.Account.ID, s.Key.User.Concurrency, s.Account.Concurrency) != nil {
		c.Status(503)
		return
	}
	l.LastBeat = time.Now().Unix()
	if e.store.Save(l.ID, &l, false) != nil {
		c.Status(503)
		return
	}
	if in.TurnStateReceived {
		c.Set("api_key", k)
		e.h.gatewayService.TrackEdgeTurnState(c, &s.Account)
	}
	c.JSON(200, gin.H{"ok": true})
}
func (e *EdgeControl) Settle(c *gin.Context) {
	var receipt edgebridge.Receipt
	if c.ShouldBindJSON(&receipt) != nil || receipt.Validate() != nil {
		c.Status(400)
		return
	}
	state, err := e.settle(c.Request.Context(), receipt)
	if err != nil {
		c.JSON(503, gin.H{"error": "settlement pending"})
		return
	}
	c.JSON(200, gin.H{"state": state})
}
func (e *EdgeControl) settle(ctx context.Context, receipt edgebridge.Receipt) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var l edgeLease
	if err := e.store.Load(receipt.ID, &l); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(receipt)
	hash := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(hash[:])
	if l.ReceiptHash != "" && l.ReceiptHash != fingerprint {
		return "", errors.New("receipt conflict")
	}
	if l.State == "settled" || l.State == "uncertain" {
		return l.State, nil
	}
	l.Receipt = &receipt
	l.ReceiptHash = fingerprint
	l.State = "pending"
	if err := e.store.Save(l.ID, &l, false); err != nil {
		return "", err
	}
	if receipt.Outcome == "unknown" {
		if err := e.release(ctx, &l); err != nil {
			return "", err
		}
		l.State = "uncertain"
		return l.State, e.store.Save(l.ID, &l, false)
	}
	s := l.Snapshot
	if l.Bill == nil {
		result := &service.OpenAIForwardResult{RequestID: "edge:" + l.ID, Model: s.Model, UpstreamModel: l.UpstreamModel, UpstreamResponseModel: receipt.Model, Stream: s.Stream,
			Usage:    service.OpenAIUsage{InputTokens: receipt.InputTokens, ImageInputTokens: receipt.ImageInputTokens, OutputTokens: receipt.OutputTokens, CacheReadInputTokens: receipt.CachedTokens, CacheCreationInputTokens: receipt.CacheWriteTokens},
			Duration: time.Duration(receipt.DurationMS) * time.Millisecond, FirstTokenMs: edgeFirstTokenMillis(receipt.FirstTokenMS), ReasoningEffort: s.Effort, ServiceTier: s.Tier}
		err := e.h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{Result: result, APIKey: &s.Key, User: s.Key.User, Account: &s.Account,
			InboundEndpoint: "/v1/responses", UpstreamEndpoint: "/backend-api/codex/responses", UserAgent: s.UserAgent, IPAddress: s.IP, RequestPayloadHash: s.PayloadHash,
			APIKeyService: e.h.apiKeyService, QuotaPlatform: service.PlatformOpenAI, PricingAt: s.PricingAt, ChannelUsageFields: s.Fields, EdgeRequestID: "edge:" + l.ID, EdgeRateMultiplier: &s.Rate,
			EdgeUpstreamStatus: receipt.Status,
			CyberBlocked:       receipt.ErrorCode == "cyber_policy",
			PersistEdgeBill: func(cmd *service.UsageBillingCommand, log *service.UsageLog) error {
				l.Bill = cmd
				l.Log = log
				return e.store.Save(l.ID, &l, false)
			},
		})
		if err != nil {
			return "", err
		}
	}
	if err := e.h.gatewayService.ApplyEdgeBill(ctx, l.Bill, l.Log); err != nil {
		return "", err
	}
	if err := e.h.gatewayService.RecordEdgeOutcome(ctx, "edge:"+l.ID, s.Key.ID, service.UsageOutcome{Outcome: receipt.Outcome, ErrorCode: receipt.ErrorCode, FirstOutputMS: receipt.FirstOutputMS}); err != nil {
		return "", err
	}
	e.h.apiKeyService.InvalidateAuthCacheByUserID(ctx, s.Key.UserID)
	if s.Key.GroupID != nil {
		if err := e.h.gatewayService.BindEdgeResponse(ctx, *s.Key.GroupID, s.Account.ID, receipt.ResponseID); err != nil {
			return "", err
		}
	}
	if !l.Observed {
		l.Observed = true
		if err := e.store.Save(l.ID, &l, false); err != nil {
			return "", err
		}
		e.h.gatewayService.ObserveEdgeResponse(ctx, &s.Account, receipt.Status, receipt.Headers)
		if receipt.ErrorCode == "cyber_policy" {
			e.h.gatewayService.MarkCyberSessionBlocked(ctx, s.CyberKey)
		}
	}
	if err := e.release(ctx, &l); err != nil {
		return "", err
	}
	l.State = "settled"
	return l.State, e.store.Save(l.ID, &l, false)
}
func (e *EdgeControl) recoverLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ids, err := e.store.IDs()
		if err != nil {
			continue
		}
		for _, id := range ids {
			var l edgeLease
			if e.store.Load(id, &l) != nil {
				continue
			}
			if l.State == "pending" && l.Receipt != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_, _ = e.settle(ctx, *l.Receipt)
				cancel()
				continue
			}
			if (l.State == "authorized" || l.State == "preparing") && (time.Now().Unix() > l.Deadline || time.Now().Unix()-l.LastBeat > 60) {
				e.mu.Lock()
				var current edgeLease
				if e.store.Load(id, &current) == nil && (current.State == "authorized" || current.State == "preparing") && (time.Now().Unix() > current.Deadline || time.Now().Unix()-current.LastBeat > 60) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					if e.release(ctx, &current) == nil {
						current.State = "expired"
						_ = e.store.Save(id, &current, false)
					}
					cancel()
				}
				e.mu.Unlock()
			}
		}
	}
}
