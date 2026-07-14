package dbstore

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

const sqliteUnixMicroExpression = "CAST(unixepoch('subsec') * 1000000 AS INTEGER)"

func (store *Store) databaseNow(db *gorm.DB) (time.Time, error) {
	var query string
	switch store.dialect {
	case "sqlite":
		query = "SELECT " + sqliteUnixMicroExpression
	case "mysql":
		query = "SELECT CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(6)) * 1000000 AS SIGNED)"
	case "postgres":
		query = "SELECT CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000000 AS BIGINT)"
	default:
		return time.Time{}, fmt.Errorf("dbstore: database clock is unsupported for dialect %q", store.dialect)
	}
	var unixMicro int64
	if err := db.Raw(query).Scan(&unixMicro).Error; err != nil {
		return time.Time{}, fmt.Errorf("dbstore: read %s database clock: %w", store.dialect, err)
	}
	if unixMicro <= 0 {
		return time.Time{}, fmt.Errorf("dbstore: %s database clock returned %d", store.dialect, unixMicro)
	}
	return time.UnixMicro(unixMicro).UTC(), nil
}

func resolveLeaseExpiry(now, supplied time.Time, duration time.Duration) (time.Time, error) {
	if duration < 0 {
		return time.Time{}, fmt.Errorf("lease duration must not be negative")
	}
	if duration > 0 {
		return now.Add(duration), nil
	}
	return supplied, nil
}
