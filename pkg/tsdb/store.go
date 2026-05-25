package tsdb

import "time"

// Store 定义 TSDB 存储后端的统一接口
// SQL 模式和 VictoriaMetrics 模式都实现此接口
type Store interface {
	// 写入方法
	WriteServerMetrics(m *ServerMetrics) error
	WriteServiceMetrics(m *ServiceMetrics) error

	// 查询方法
	QueryServiceHistory(serviceID uint64, period QueryPeriod) (*ServiceHistoryResult, error)
	QueryServiceDailyStats(serviceID uint64, today time.Time, days int) ([]DailyServiceStats, error)
	// QueryServicesDailyStats 批量查询多个服务的每日统计，返回 map[serviceID][]DailyServiceStats，
	// 用于服务监控总览启动时一次拉取，避免按服务循环导致 N×聚合 SQL 的 N+1 模式。
	QueryServicesDailyStats(serviceIDs []uint64, today time.Time, days int) (map[uint64][]DailyServiceStats, error)
	QueryServerMetrics(serverID uint64, metric MetricType, period QueryPeriod) ([]MetricDataPoint, error)
	QueryServiceHistoryByServerID(serverID uint64, period QueryPeriod) (map[uint64]*ServiceHistoryResult, error)

	// 生命周期方法
	Maintenance()
	Flush()
	Close() error
	IsClosed() bool
}
