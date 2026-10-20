package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestUsageLogExportCountsEmptyFirstBatch(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*), COALESCE(MAX(id), 0) FROM usage_logs WHERE user_id = $1")).
		WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"count", "max_id"}).AddRow(0, 0))
	logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{PageSize: 1000}, usagestats.UsageLogFilters{UserID: 42, Export: true})
	require.NoError(t, err)
	require.Empty(t, logs)
	require.Zero(t, page.Total)
	require.Empty(t, page.NextCursor)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUsageLogExportContinuationSkipsCountAndOffset(t *testing.T) {
	for _, sortBy := range []string{"id", "created_at", "model"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(sortBy+"_"+order, func(t *testing.T) {
				db, mock := newSQLMock(t)
				repo := &usageLogRepository{sql: db}
				cursor := &usagestats.UsageLogExportCursor{MaxID: 100, ID: 42, CreatedAt: time.Now().UTC(), Model: "a-model"}
				comparison := "<"
				if order == "asc" {
					comparison = ">"
				}
				condition := "id " + comparison + " $3"
				args := []any{int64(7), cursor.MaxID, cursor.ID, 1001}
				switch sortBy {
				case "created_at":
					condition = "(created_at, id) " + comparison + " ($3, $4)"
					args = []any{int64(7), cursor.MaxID, cursor.CreatedAt, cursor.ID, 1001}
				case "model":
					condition = "(COALESCE(NULLIF(TRIM(requested_model), ''), model), id) " + comparison + " ($3, $4)"
					args = []any{int64(7), cursor.MaxID, cursor.Model, cursor.ID, 1001}
				}
				mock.ExpectQuery(regexp.QuoteMeta("WHERE user_id = $1 AND id <= $2 AND " + condition + " ORDER BY")).
					WithArgs(anySliceToDriverValues(args)...).WillReturnRows(sqlmock.NewRows([]string{"id"}))
				logs, page, err := repo.ListWithFilters(context.Background(), pagination.PaginationParams{Page: 999, PageSize: 1000, SortBy: sortBy, SortOrder: order}, usagestats.UsageLogFilters{UserID: 7, ExactTotal: true, Export: true, ExportCursor: cursor})
				require.NoError(t, err)
				require.Empty(t, logs)
				require.Empty(t, page.NextCursor)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
