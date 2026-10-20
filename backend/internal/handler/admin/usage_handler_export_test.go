package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestAdminUsageExportCursor(t *testing.T) {
	cursor := usagestats.UsageLogExportCursor{
		MaxID: 100, ID: 42, CreatedAt: time.Date(2026, 10, 20, 0, 0, 0, 123456000, time.UTC),
		SortBy: "created_at", SortOrder: "desc",
	}
	raw, err := cursor.Encode()
	require.NoError(t, err)
	repo := &adminUsageRepoCapture{}
	router := newAdminUsageRequestTypeTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/admin/usage?export=true&page=9&page_size=1000&user_id=7&export_cursor="+raw, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, repo.listFilters.Export)
	require.Equal(t, cursor, *repo.listFilters.ExportCursor)
	require.Equal(t, int64(7), repo.listFilters.UserID)
	require.Equal(t, 1, repo.listParams.Page)
	require.Equal(t, 1000, repo.listParams.PageSize)
	require.Contains(t, rec.Body.String(), `"next_cursor":""`)

	for _, query := range []string{
		"export=bad", "export_cursor=" + raw,
		"export=true&export_cursor=bad",
		"export=true&sort_order=asc&export_cursor=" + raw,
		"export=true&sort_by=model&export_cursor=" + raw,
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/usage?"+query, nil))
		require.Equal(t, http.StatusBadRequest, rec.Code, query)
	}
}
