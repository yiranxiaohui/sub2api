//go:build integration

package repository

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func (s *UsageLogRepoSuite) TestExportCursorPagination() {
	for _, sortBy := range []string{"id", "created_at", "model"} {
		for _, order := range []string{"asc", "desc"} {
			s.Run(sortBy+"_"+order, func() {
				user := mustCreateUser(s.T(), s.client, &service.User{Email: uuid.NewString() + "@export.test"})
				key := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: uuid.NewString(), Name: "export-key"})
				account := mustCreateAccount(s.T(), s.client, &service.Account{Name: "export-account"})
				base := time.Date(2026, 10, 20, 0, 0, 0, 123456000, time.UTC)
				for i, model := range []string{"z-model", "a-model", "a-model", "m-model", "\t", ""} {
					log := s.createUsageLog(user, key, account, 10, 20, 0.1, base.Add(time.Duration(i%3)*time.Microsecond))
					_, err := s.repo.sql.ExecContext(s.ctx, "UPDATE usage_logs SET requested_model = $1, model = $2 WHERE id = $3", " "+model+" ", " legacy-model ", log.ID)
					s.Require().NoError(err)
				}
				params := pagination.PaginationParams{Page: 1, PageSize: 100, SortBy: sortBy, SortOrder: order}
				filters := usagestats.UsageLogFilters{UserID: user.ID, ExactTotal: true}
				expected, _, err := s.repo.ListWithFilters(s.ctx, params, filters)
				s.Require().NoError(err)
				var expectedIDs []int64
				for _, log := range expected {
					expectedIDs = append(expectedIDs, log.ID)
				}
				filters.Export = true
				params.PageSize = 2
				var gotIDs []int64
				for batch := 0; ; batch++ {
					s.Require().Less(batch, 5, "export must terminate")
					logs, page, err := s.repo.ListWithFilters(s.ctx, params, filters)
					s.Require().NoError(err)
					for _, log := range logs {
						s.Require().Equal("export-key", log.APIKey.Name)
						gotIDs = append(gotIDs, log.ID)
					}
					if batch == 0 {
						s.Require().Equal(int64(6), page.Total)
						s.createUsageLog(user, key, account, 10, 20, 0.1, base)
						// Continuation must still work if the row holding the cursor is deleted.
						s.Require().NoError(s.repo.Delete(s.ctx, logs[len(logs)-1].ID))
					} else {
						s.Require().Zero(page.Total)
					}
					if page.NextCursor == "" {
						break
					}
					filters.ExportCursor, err = usagestats.ParseUsageLogExportCursor(page.NextCursor, params)
					s.Require().NoError(err)
				}
				s.Require().Equal(expectedIDs, gotIDs)
			})
		}
	}
}
