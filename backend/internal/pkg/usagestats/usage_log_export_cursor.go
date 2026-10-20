package usagestats

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// UsageLogExportCursor preserves the database timestamp precision and bounds
// later batches to IDs that existed when the export started.
type UsageLogExportCursor struct {
	MaxID     int64     `json:"max_id"`
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Model     string    `json:"model"`
	SortBy    string    `json:"sort_by"`
	SortOrder string    `json:"sort_order"`
}

func NormalizeUsageLogSortBy(sortBy string) string {
	switch strings.ToLower(strings.TrimSpace(sortBy)) {
	case "created_at":
		return "created_at"
	case "model":
		return "model"
	default:
		return "id"
	}
}

func (c UsageLogExportCursor) Encode() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func ParseUsageLogExportCursor(raw string, params pagination.PaginationParams) (*UsageLogExportCursor, error) {
	if len(raw) > 4096 {
		return nil, fmt.Errorf("invalid export_cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid export_cursor")
	}
	var cursor UsageLogExportCursor
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.ID <= 0 || cursor.MaxID < cursor.ID ||
		cursor.SortBy != NormalizeUsageLogSortBy(params.SortBy) ||
		cursor.SortOrder != params.NormalizedSortOrder(pagination.SortOrderDesc) ||
		(cursor.SortBy == "created_at" && cursor.CreatedAt.IsZero()) {
		return nil, fmt.Errorf("invalid export_cursor")
	}
	return &cursor, nil
}
