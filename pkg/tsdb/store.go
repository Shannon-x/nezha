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
	QueryServerMetrics(serverID uint64, metric MetricType, period QueryPeriod) ([]MetricDataPoint, error)
	QueryServiceHistoryByServerID(serverID uint64, period QueryPeriod) (map[uint64]*ServiceHistoryResult, error)

	// 生命周期方法
	Maintenance()
	Flush()
	Close() error
	IsClosed() bool
}
