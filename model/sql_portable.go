package model

import (
	"strings"

	"gorm.io/gorm"
)

// fork: 多数据库下可移植的 SQL 片段。上游只支持 SQLite，原生 SQL 里用了
// datetime() 和反引号，MySQL 没有 datetime() 函数，PostgreSQL / SQL Server
// 不认反引号。

// TimeCompareExpr 返回比较时间列与一个参数的条件，如 created_at >= ?。
// SQLite 以文本存储时间，不同时区写入的值按字典序比较会出错，保留上游的
// datetime() 归一；其他数据库的时间列可直接比较。
func TimeCompareExpr(db *gorm.DB, column, op string) string {
	quoted := db.Statement.Quote(column)
	if db.Dialector.Name() == "sqlite" {
		return "datetime(" + quoted + ") " + op + " datetime(?)"
	}
	return quoted + " " + op + " ?"
}

// sumColumnsExpr 返回 SUM(a+b) AS n，列名按方言加引号（in / out 是保留字）。
func sumColumnsExpr(db *gorm.DB, columns ...string) string {
	quoted := make([]string, len(columns))
	for i, c := range columns {
		quoted[i] = db.Statement.Quote(c)
	}
	return "SUM(" + strings.Join(quoted, "+") + ") AS n"
}

// cycleTransferWhere 是流量周期统计的条件：[start, end) 且不晚于评估时刻。
func cycleTransferWhere(db *gorm.DB) string {
	return TimeCompareExpr(db, "created_at", ">=") + " AND " +
		TimeCompareExpr(db, "created_at", "<") + " AND " +
		TimeCompareExpr(db, "created_at", "<=") + " AND server_id = ?"
}
