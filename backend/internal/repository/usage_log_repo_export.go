package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) listUsageLogsForExport(ctx context.Context, conditions []string, args []any, params pagination.PaginationParams, cursor *usagestats.UsageLogExportCursor) ([]service.UsageLog, *pagination.PaginationResult, error) {
	var total, maxID int64
	if cursor == nil {
		// Count once and capture an ID boundary so new requests do not shift
		// subsequent batches or keep extending an export of today's records.
		query := "SELECT COUNT(*), COALESCE(MAX(id), 0) FROM usage_logs " + buildWhere(conditions)
		if err := scanSingleRow(ctx, r.sql, query, args, &total, &maxID); err != nil {
			return nil, nil, err
		}
	} else {
		maxID = cursor.MaxID
	}
	page := paginationResultFromTotal(total, params)
	if maxID == 0 {
		return []service.UsageLog{}, page, nil
	}

	conditions = append(conditions, fmt.Sprintf("id <= $%d", len(args)+1))
	args = append(args, maxID)
	if cursor != nil {
		comparison := "<"
		if params.NormalizedSortOrder(pagination.SortOrderDesc) == pagination.SortOrderAsc {
			comparison = ">"
		}
		switch usagestats.NormalizeUsageLogSortBy(params.SortBy) {
		case "created_at":
			conditions = append(conditions, fmt.Sprintf("(created_at, id) %s ($%d, $%d)", comparison, len(args)+1, len(args)+2))
			args = append(args, cursor.CreatedAt, cursor.ID)
		case "model":
			conditions = append(conditions, fmt.Sprintf("(COALESCE(NULLIF(TRIM(requested_model), ''), model), id) %s ($%d, $%d)", comparison, len(args)+1, len(args)+2))
			args = append(args, cursor.Model, cursor.ID)
		default:
			conditions = append(conditions, fmt.Sprintf("id %s $%d", comparison, len(args)+1))
			args = append(args, cursor.ID)
		}
	}
	// Read the sort key directly from SQL. Display-model normalization can differ
	// from PostgreSQL TRIM for historical records containing whitespace.
	query := fmt.Sprintf("SELECT %s, COALESCE(NULLIF(TRIM(requested_model), ''), model) FROM usage_logs %s ORDER BY %s LIMIT $%d", usageLogSelectColumns, buildWhere(conditions), usageLogOrderBy(params), len(args)+1)
	logs, lastModel, err := r.queryUsageLogsForExport(ctx, query, append(args, params.Limit()+1), params.Limit())
	if err != nil {
		return nil, nil, err
	}
	if len(logs) > params.Limit() {
		logs = logs[:params.Limit()]
		last := logs[len(logs)-1]
		page.NextCursor, err = (usagestats.UsageLogExportCursor{
			MaxID: maxID, ID: last.ID, CreatedAt: last.CreatedAt, Model: lastModel,
			SortBy: usagestats.NormalizeUsageLogSortBy(params.SortBy), SortOrder: params.NormalizedSortOrder(pagination.SortOrderDesc),
		}).Encode()
		if err != nil {
			return nil, nil, err
		}
	}
	return logs, page, nil
}

type usageLogExportScanner struct {
	rows  *sql.Rows
	model *string
}

func (s usageLogExportScanner) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.model)...)
}

func (r *usageLogRepository) queryUsageLogsForExport(ctx context.Context, query string, args []any, limit int) (logs []service.UsageLog, lastModel string, err error) {
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			logs, lastModel, err = nil, "", closeErr
		}
	}()
	logs = make([]service.UsageLog, 0, limit+1)
	for rows.Next() {
		var model string
		log, err := scanUsageLog(usageLogExportScanner{rows: rows, model: &model})
		if err != nil {
			return nil, "", err
		}
		logs = append(logs, *log)
		if len(logs) <= limit {
			lastModel = model
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return logs, lastModel, nil
}
