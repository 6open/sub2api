package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strings"
)

func (r *usageLogRepository) SetUsageOutcome(ctx context.Context, request string, key int64, v service.UsageOutcome) error {
	result, err := r.sql.ExecContext(ctx, `INSERT INTO lklb_usage_outcomes(usage_log_id,outcome,error_code,first_output_ms)
 SELECT id,$3,$4,$5 FROM usage_logs WHERE request_id=$1 AND api_key_id=$2
 ON CONFLICT(usage_log_id) DO UPDATE SET outcome=excluded.outcome,error_code=excluded.error_code,first_output_ms=excluded.first_output_ms`, request, key, v.Outcome, v.ErrorCode, v.FirstOutputMS)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fmt.Errorf("usage row missing")
	}
	return err
}
func (r *usageLogRepository) GetUsageOutcomes(ctx context.Context, ids []int64) (map[int64]service.UsageOutcome, error) {
	out := map[int64]service.UsageOutcome{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	rows, err := r.sql.QueryContext(ctx, "SELECT usage_log_id,outcome,error_code,first_output_ms FROM lklb_usage_outcomes WHERE usage_log_id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var v service.UsageOutcome
		var first sql.NullInt64
		if err := rows.Scan(&id, &v.Outcome, &v.ErrorCode, &first); err != nil {
			return nil, err
		}
		if first.Valid {
			n := int(first.Int64)
			v.FirstOutputMS = &n
		}
		out[id] = v
	}
	return out, rows.Err()
}
