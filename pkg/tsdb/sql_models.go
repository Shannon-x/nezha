package tsdb

import "time"

// TSDBServerMetric 服务器指标数据表
type TSDBServerMetric struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement" json:"-"`
	ServerID   uint64    `gorm:"index:idx_tsdb_srv_metric,priority:1;not null" json:"server_id"`
	MetricName string    `gorm:"type:varchar(64);index:idx_tsdb_srv_metric,priority:2;not null" json:"metric_name"`
	Value      float64   `gorm:"not null" json:"value"`
	CreatedAt  time.Time `gorm:"index:idx_tsdb_srv_metric,priority:3;not null" json:"created_at"`
}

func (TSDBServerMetric) TableName() string {
	return "tsdb_server_metrics"
}

// TSDBServiceMetric 服务监控指标数据表
type TSDBServiceMetric struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"-"`
	ServiceID uint64    `gorm:"index:idx_tsdb_svc_srv,priority:1;index:idx_tsdb_svc_time,priority:1;not null" json:"service_id"`
	ServerID  uint64    `gorm:"index:idx_tsdb_svc_srv,priority:2;not null" json:"server_id"`
	Delay     float64   `gorm:"not null" json:"delay"`
	Status    uint8     `gorm:"not null" json:"status"` // 1=up, 0=down
	CreatedAt time.Time `gorm:"index:idx_tsdb_svc_srv,priority:3;index:idx_tsdb_svc_time,priority:2;not null" json:"created_at"`
}

func (TSDBServiceMetric) TableName() string {
	return "tsdb_service_metrics"
}
