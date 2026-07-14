package dbstore

import (
	"errors"

	"github.com/go-sql-driver/mysql"
)

const (
	mysqlLockWaitTimeoutCode = 1205
	mysqlLockDeadlockCode    = 1213
	mysqlLockNoWaitCode      = 3572
)

func mysqlConcurrencyError(err error) bool {
	var target *mysql.MySQLError
	if !errors.As(err, &target) {
		return false
	}
	return target.Number == mysqlLockWaitTimeoutCode ||
		target.Number == mysqlLockDeadlockCode ||
		target.Number == mysqlLockNoWaitCode
}
