package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"
)

// LDCShopHandler proxies/lists LinuxDO credit shop purchase orders for admins.
type LDCShopHandler struct {
	httpClient *http.Client
}

// NewLDCShopHandler creates a new LDC shop admin handler.
func NewLDCShopHandler() *LDCShopHandler {
	return &LDCShopHandler{
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}
}

type ldcShopOrder struct {
	ID               int64   `json:"id"`
	OutTradeNo       string  `json:"out_trade_no"`
	Plan             string  `json:"plan"`
	LDCAmount        string  `json:"ldc_amount"`
	USDValue         float64 `json:"usd_value"`
	UserSub          string  `json:"user_sub"`
	Username         string  `json:"username"`
	Status           string  `json:"status"`
	CreditTradeNo    string  `json:"credit_trade_no"`
	Code             string  `json:"code"`
	Sub2APIUserID    *int64  `json:"sub2api_user_id"`
	Sub2APIUserEmail string  `json:"sub2api_user_email"`
	DeliveryMessage  string  `json:"delivery_message"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
}

type ldcShopListResult struct {
	Items        []ldcShopOrder   `json:"items"`
	Total        int64            `json:"total"`
	Page         int              `json:"page"`
	PageSize     int              `json:"page_size"`
	Pages        int              `json:"pages"`
	StatusCounts map[string]int64 `json:"status_counts"`
}

// List handles GET /api/v1/admin/ldc-shop/orders
func (h *LDCShopHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	status := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("search"))
	if search == "" {
		search = strings.TrimSpace(c.Query("q"))
	}
	if len(search) > 100 {
		search = search[:100]
	}

	result, err := h.listOrders(c.Request.Context(), page, pageSize, status, search)
	if err != nil {
		response.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	response.Success(c, result)
}

func (h *LDCShopHandler) listOrders(ctx context.Context, page, pageSize int, status, search string) (*ldcShopListResult, error) {
	if dbPath := strings.TrimSpace(os.Getenv("LKLB_CODE_SHOP_DB_PATH")); dbPath != "" {
		return h.listFromDB(dbPath, page, pageSize, status, search)
	}
	baseURL := strings.TrimSpace(os.Getenv("LKLB_CODE_SHOP_URL"))
	token := strings.TrimSpace(os.Getenv("LKLB_CODE_SHOP_ADMIN_TOKEN"))
	if baseURL != "" && token != "" {
		return h.listFromHTTP(ctx, baseURL, token, page, pageSize, status, search)
	}
	return nil, fmt.Errorf("LDC shop is not configured (set LKLB_CODE_SHOP_DB_PATH or LKLB_CODE_SHOP_URL+LKLB_CODE_SHOP_ADMIN_TOKEN)")
}

func (h *LDCShopHandler) listFromDB(dbPath string, page, pageSize int, status, search string) (*ldcShopListResult, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("code shop database not found: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(3000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open code shop db: %w", err)
	}
	defer func() { _ = db.Close() }()

	where := make([]string, 0, 2)
	args := make([]any, 0, 8)
	if status != "" {
		where = append(where, "status = ?")
		args = append(args, status)
	}
	if search != "" {
		like := "%" + search + "%"
		where = append(where, "(out_trade_no LIKE ? OR username LIKE ? OR IFNULL(code,'') LIKE ? OR IFNULL(sub2api_user_email,'') LIKE ? OR IFNULL(user_sub,'') LIKE ? OR IFNULL(credit_trade_no,'') LIKE ?)")
		args = append(args, like, like, like, like, like, like)
	}
	whereSQL := ""
	if len(where) > 0 {
		whereSQL = " WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := db.QueryRow("SELECT COUNT(*) FROM purchase_orders"+whereSQL, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count purchase_orders: %w", err)
	}

	offset := (page - 1) * pageSize
	queryArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := db.Query(`
SELECT id, out_trade_no, plan, ldc_amount, usd_value, user_sub, username, status,
       credit_trade_no, code, sub2api_user_id, sub2api_user_email, delivery_message,
       created_at, updated_at
FROM purchase_orders`+whereSQL+` ORDER BY id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query purchase_orders: %w", err)
	}
	defer func() { _ = rows.Close() }()

	items := make([]ldcShopOrder, 0, pageSize)
	for rows.Next() {
		var item ldcShopOrder
		var creditTradeNo, code, email, delivery sql.NullString
		var userID sql.NullInt64
		var usd any
		if err := rows.Scan(
			&item.ID, &item.OutTradeNo, &item.Plan, &item.LDCAmount, &usd, &item.UserSub, &item.Username, &item.Status,
			&creditTradeNo, &code, &userID, &email, &delivery, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan purchase_orders: %w", err)
		}
		item.USDValue = toFloat64(usd)
		item.CreditTradeNo = creditTradeNo.String
		item.Code = code.String
		item.Sub2APIUserEmail = email.String
		item.DeliveryMessage = delivery.String
		if userID.Valid {
			v := userID.Int64
			item.Sub2APIUserID = &v
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	statusCounts := map[string]int64{}
	countRows, err := db.Query(`SELECT status, COUNT(*) FROM purchase_orders GROUP BY status`)
	if err == nil {
		defer func() { _ = countRows.Close() }()
		for countRows.Next() {
			var st string
			var n int64
			if err := countRows.Scan(&st, &n); err == nil {
				statusCounts[st] = n
			}
		}
	}

	pages := int((total + int64(pageSize) - 1) / int64(pageSize))
	if pages < 1 {
		pages = 1
	}
	return &ldcShopListResult{
		Items:        items,
		Total:        total,
		Page:         page,
		PageSize:     pageSize,
		Pages:        pages,
		StatusCounts: statusCounts,
	}, nil
}

func (h *LDCShopHandler) listFromHTTP(ctx context.Context, baseURL, token string, page, pageSize int, status, search string) (*ldcShopListResult, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/admin/orders")
	if err != nil {
		return nil, fmt.Errorf("invalid LKLB_CODE_SHOP_URL: %w", err)
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	q.Set("page_size", strconv.Itoa(pageSize))
	if status != "" {
		q.Set("status", status)
	}
	if search != "" {
		q.Set("search", search)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Admin-Token", token)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request code shop failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("code shop returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		OK           bool             `json:"ok"`
		Error        string           `json:"error"`
		Items        []ldcShopOrder   `json:"items"`
		Total        int64            `json:"total"`
		Page         int              `json:"page"`
		PageSize     int              `json:"page_size"`
		Pages        int              `json:"pages"`
		StatusCounts map[string]int64 `json:"status_counts"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode code shop response: %w", err)
	}
	if !payload.OK {
		msg := payload.Error
		if msg == "" {
			msg = "code shop request failed"
		}
		return nil, fmt.Errorf("%s", msg)
	}
	if payload.Items == nil {
		payload.Items = []ldcShopOrder{}
	}
	if payload.StatusCounts == nil {
		payload.StatusCounts = map[string]int64{}
	}
	return &ldcShopListResult{
		Items:        payload.Items,
		Total:        payload.Total,
		Page:         payload.Page,
		PageSize:     payload.PageSize,
		Pages:        payload.Pages,
		StatusCounts: payload.StatusCounts,
	}, nil
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case []byte:
		f, _ := strconv.ParseFloat(string(n), 64)
		return f
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}
