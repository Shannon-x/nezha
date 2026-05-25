package tsdb

import (
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"
)

// SQLStore 基于关系型数据库的 TSDB 存储实现
type SQLStore struct {
	db     *gorm.DB
	config *Config
	mu     sync.RWMutex
	closed bool

	// 写入缓冲
	serverBuf  []TSDBServerMetric
	serviceBuf []TSDBServiceMetric
	bufMu      sync.Mutex
	bufSize    int
	flushTick  *time.Ticker
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// OpenSQL 创建基于 SQL 的 TSDB 存储
func OpenSQL(db *gorm.DB, config *Config) (*SQLStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection is nil")
	}
	if config == nil {
		config = DefaultConfig()
	}
	config.Validate()

	// 自动建表
	if err := db.AutoMigrate(&TSDBServerMetric{}, &TSDBServiceMetric{}); err != nil {
		return nil, fmt.Errorf("failed to migrate TSDB tables: %w", err)
	}

	bufSize := config.WriteBufferSize
	if bufSize <= 0 {
		bufSize = 512
	}

	flushInterval := config.WriteBufferFlushInterval
	if flushInterval <= 0 {
		flushInterval = 5 * time.Second
	}

	s := &SQLStore{
		db:      db,
		config:  config,
		bufSize: bufSize,
		stopCh:  make(chan struct{}),
	}
	s.flushTick = time.NewTicker(flushInterval)

	s.wg.Add(1)
	go s.flushLoop()

	log.Printf("NEZHA>> TSDB (SQL mode) opened, retention: %d days, buffer: %d",
		config.RetentionDays, bufSize)

	return s, nil
}

func (s *SQLStore) flushLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.flushTick.C:
			s.Flush()
		case <-s.stopCh:
			s.Flush()
			return
		}
	}
}

// WriteServerMetrics 写入服务器指标
func (s *SQLStore) WriteServerMetrics(m *ServerMetrics) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fmt.Errorf("SQLStore is closed")
	}

	ts := m.Timestamp
	sid := m.ServerID

	rows := []TSDBServerMetric{
		{ServerID: sid, MetricName: string(MetricServerCPU), Value: m.CPU, CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerMemory), Value: float64(m.MemUsed), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerSwap), Value: float64(m.SwapUsed), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerDisk), Value: float64(m.DiskUsed), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerNetInSpeed), Value: float64(m.NetInSpeed), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerNetOutSpeed), Value: float64(m.NetOutSpeed), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerNetInTransfer), Value: float64(m.NetInTransfer), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerNetOutTransfer), Value: float64(m.NetOutTransfer), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerLoad1), Value: m.Load1, CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerLoad5), Value: m.Load5, CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerLoad15), Value: m.Load15, CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerTCPConn), Value: float64(m.TCPConnCount), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerUDPConn), Value: float64(m.UDPConnCount), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerProcessCount), Value: float64(m.ProcessCount), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerTemperature), Value: m.Temperature, CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerUptime), Value: float64(m.Uptime), CreatedAt: ts},
		{ServerID: sid, MetricName: string(MetricServerGPU), Value: m.GPU, CreatedAt: ts},
	}

	s.bufMu.Lock()
	s.serverBuf = append(s.serverBuf, rows...)
	shouldFlush := len(s.serverBuf) >= s.bufSize
	s.bufMu.Unlock()

	if shouldFlush {
		s.flushServerBuf()
	}
	return nil
}

// WriteServiceMetrics 写入服务监控指标
func (s *SQLStore) WriteServiceMetrics(m *ServiceMetrics) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fmt.Errorf("SQLStore is closed")
	}

	var status uint8
	if m.Successful {
		status = 1
	}

	row := TSDBServiceMetric{
		ServiceID: m.ServiceID,
		ServerID:  m.ServerID,
		Delay:     m.Delay,
		Status:    status,
		CreatedAt: m.Timestamp,
	}

	s.bufMu.Lock()
	s.serviceBuf = append(s.serviceBuf, row)
	shouldFlush := len(s.serviceBuf) >= s.bufSize
	s.bufMu.Unlock()

	if shouldFlush {
		s.flushServiceBuf()
	}
	return nil
}

func (s *SQLStore) flushServerBuf() {
	s.bufMu.Lock()
	if len(s.serverBuf) == 0 {
		s.bufMu.Unlock()
		return
	}
	rows := s.serverBuf
	s.serverBuf = nil
	s.bufMu.Unlock()

	if err := s.db.CreateInBatches(rows, 200).Error; err != nil {
		log.Printf("NEZHA>> TSDB SQL: failed to flush server metrics: %v", err)
	}
}

func (s *SQLStore) flushServiceBuf() {
	s.bufMu.Lock()
	if len(s.serviceBuf) == 0 {
		s.bufMu.Unlock()
		return
	}
	rows := s.serviceBuf
	s.serviceBuf = nil
	s.bufMu.Unlock()

	if err := s.db.CreateInBatches(rows, 200).Error; err != nil {
		log.Printf("NEZHA>> TSDB SQL: failed to flush service metrics: %v", err)
	}
}

// Flush 刷新所有缓冲数据到数据库
func (s *SQLStore) Flush() {
	s.flushServerBuf()
	s.flushServiceBuf()
}

// Close 关闭存储
func (s *SQLStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.flushTick.Stop()
	close(s.stopCh)
	s.wg.Wait()
	s.closed = true
	log.Println("NEZHA>> TSDB (SQL mode) closed")
	return nil
}

// IsClosed 检查是否已关闭
func (s *SQLStore) IsClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// Maintenance 执行维护：清理过期数据
func (s *SQLStore) Maintenance() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}

	log.Println("NEZHA>> TSDB SQL: starting maintenance...")

	cutoff := time.Now().AddDate(0, 0, -int(s.config.RetentionDays))

	if result := s.db.Where("created_at < ?", cutoff).Delete(&TSDBServerMetric{}); result.Error != nil {
		log.Printf("NEZHA>> TSDB SQL: failed to clean server metrics: %v", result.Error)
	} else if result.RowsAffected > 0 {
		log.Printf("NEZHA>> TSDB SQL: cleaned %d expired server metric rows", result.RowsAffected)
	}

	if result := s.db.Where("created_at < ?", cutoff).Delete(&TSDBServiceMetric{}); result.Error != nil {
		log.Printf("NEZHA>> TSDB SQL: failed to clean service metrics: %v", result.Error)
	} else if result.RowsAffected > 0 {
		log.Printf("NEZHA>> TSDB SQL: cleaned %d expired service metric rows", result.RowsAffected)
	}

	log.Println("NEZHA>> TSDB SQL: maintenance completed")
}

// QueryServiceHistory 查询服务监控历史
func (s *SQLStore) QueryServiceHistory(serviceID uint64, period QueryPeriod) (*ServiceHistoryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("SQLStore is closed")
	}

	since := time.Now().Add(-period.Duration())

	rows, err := s.db.Model(&TSDBServiceMetric{}).
		Select("server_id, delay, status, created_at").
		Where("service_id = ? AND created_at >= ?", serviceID, since).
		Order("server_id, created_at").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &ServiceHistoryResult{
		ServiceID: serviceID,
		Servers:   make([]ServerServiceStats, 0),
	}

	// 按 server_id 分组
	grouped := make(map[uint64][]rawDataPoint)
	for rows.Next() {
		var serverID uint64
		var delay float64
		var status uint8
		var createdAt time.Time
		if err := rows.Scan(&serverID, &delay, &status, &createdAt); err != nil {
			continue
		}
		grouped[serverID] = append(grouped[serverID], rawDataPoint{
			timestamp: createdAt.UnixMilli(),
			value:     delay,
			status:    float64(status),
			hasDelay:  true,
			hasStatus: true,
		})
	}

	for serverID, points := range grouped {
		stats := calculateStats(points, period.DownsampleInterval())
		result.Servers = append(result.Servers, ServerServiceStats{
			ServerID: serverID,
			Stats:    stats,
		})
	}

	sort.Slice(result.Servers, func(i, j int) bool {
		return result.Servers[i].ServerID < result.Servers[j].ServerID
	})

	return result, nil
}

// QueryServiceDailyStats 查询服务每日统计
func (s *SQLStore) QueryServiceDailyStats(serviceID uint64, today time.Time, days int) ([]DailyServiceStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("SQLStore is closed")
	}

	start := today.AddDate(0, 0, -(days - 1))
	stats := make([]DailyServiceStats, days)

	type aggResult struct {
		Date     string  `gorm:"column:date"`
		Up       uint64  `gorm:"column:up"`
		Down     uint64  `gorm:"column:down"`
		AvgDelay float64 `gorm:"column:avg_delay"`
	}

	var results []aggResult
	if err := s.db.Model(&TSDBServiceMetric{}).
		Select("DATE(created_at) as date, SUM(CASE WHEN status >= 1 THEN 1 ELSE 0 END) as up, SUM(CASE WHEN status < 1 THEN 1 ELSE 0 END) as down, AVG(delay) as avg_delay").
		Where("service_id = ? AND created_at >= ? AND created_at < ?", serviceID, start, today).
		Group("DATE(created_at)").
		Find(&results).Error; err != nil {
		return nil, err
	}

	for _, r := range results {
		if len(r.Date) < 10 {
			continue
		}
		t, err := time.Parse("2006-01-02", r.Date[0:10])
		if err != nil {
			continue
		}

		// Map the aggregated date back to the stats array using precise index calculation
		dayIndex := (days - 1) - int(today.Sub(t).Hours())/24
		if dayIndex < 0 || dayIndex >= days {
			continue
		}
		stats[dayIndex].Up = r.Up
		stats[dayIndex].Down = r.Down
		stats[dayIndex].Delay = r.AvgDelay
	}

	return stats, nil
}

// QueryServicesDailyStats 批量查询多个服务的每日统计，一次聚合 SQL 取回所有结果。
//
// 相比 N 次 QueryServiceDailyStats 调用，本方法的优势：
//   - 数据库往返从 N 次降到 1 次（dashboard 启动期 N×~12s → 一次几秒）
//   - 命中 idx_tsdb_svc_time (service_id, created_at) 复合索引，service_id IN (...) 走范围扫描
//   - GROUP BY 在数据库侧聚合，Go 侧只做分桶映射
func (s *SQLStore) QueryServicesDailyStats(serviceIDs []uint64, today time.Time, days int) (map[uint64][]DailyServiceStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("SQLStore is closed")
	}

	result := make(map[uint64][]DailyServiceStats, len(serviceIDs))
	for _, id := range serviceIDs {
		result[id] = make([]DailyServiceStats, days)
	}
	if len(serviceIDs) == 0 {
		return result, nil
	}

	start := today.AddDate(0, 0, -(days - 1))

	type aggResult struct {
		ServiceID uint64  `gorm:"column:service_id"`
		Date      string  `gorm:"column:date"`
		Up        uint64  `gorm:"column:up"`
		Down      uint64  `gorm:"column:down"`
		AvgDelay  float64 `gorm:"column:avg_delay"`
	}

	var results []aggResult
	if err := s.db.Model(&TSDBServiceMetric{}).
		Select("service_id, DATE(created_at) as date, SUM(CASE WHEN status >= 1 THEN 1 ELSE 0 END) as up, SUM(CASE WHEN status < 1 THEN 1 ELSE 0 END) as down, AVG(delay) as avg_delay").
		Where("service_id IN ? AND created_at >= ? AND created_at < ?", serviceIDs, start, today).
		Group("service_id, DATE(created_at)").
		Find(&results).Error; err != nil {
		return nil, err
	}

	for _, r := range results {
		bucket, ok := result[r.ServiceID]
		if !ok {
			continue
		}
		if len(r.Date) < 10 {
			continue
		}
		t, err := time.Parse("2006-01-02", r.Date[0:10])
		if err != nil {
			continue
		}
		dayIndex := (days - 1) - int(today.Sub(t).Hours())/24
		if dayIndex < 0 || dayIndex >= days {
			continue
		}
		bucket[dayIndex].Up = r.Up
		bucket[dayIndex].Down = r.Down
		bucket[dayIndex].Delay = r.AvgDelay
	}

	return result, nil
}

// QueryServerMetrics 查询服务器指标历史
func (s *SQLStore) QueryServerMetrics(serverID uint64, metric MetricType, period QueryPeriod) ([]MetricDataPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("SQLStore is closed")
	}

	since := time.Now().Add(-period.Duration())

	rows, err := s.db.Model(&TSDBServerMetric{}).
		Select("value, created_at").
		Where("server_id = ? AND metric_name = ? AND created_at >= ?", serverID, string(metric), since).
		Order("created_at").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []rawDataPoint
	for rows.Next() {
		var value float64
		var createdAt time.Time
		if err := rows.Scan(&value, &createdAt); err != nil {
			continue
		}
		points = append(points, rawDataPoint{
			timestamp: createdAt.UnixMilli(),
			value:     value,
		})
	}

	return downsampleMetrics(points, period.DownsampleInterval(), isCumulativeMetric(metric)), nil
}

// QueryServiceHistoryByServerID 查询指定服务器的所有服务监控历史
func (s *SQLStore) QueryServiceHistoryByServerID(serverID uint64, period QueryPeriod) (map[uint64]*ServiceHistoryResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("SQLStore is closed")
	}

	since := time.Now().Add(-period.Duration())

	rows, err := s.db.Model(&TSDBServiceMetric{}).
		Select("service_id, delay, status, created_at").
		Where("server_id = ? AND created_at >= ?", serverID, since).
		Order("service_id, created_at").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// 按 service_id 分组
	grouped := make(map[uint64][]rawDataPoint)
	for rows.Next() {
		var serviceID uint64
		var delay float64
		var status uint8
		var createdAt time.Time
		if err := rows.Scan(&serviceID, &delay, &status, &createdAt); err != nil {
			continue
		}
		grouped[serviceID] = append(grouped[serviceID], rawDataPoint{
			timestamp: createdAt.UnixMilli(),
			value:     delay,
			status:    float64(status),
			hasDelay:  true,
			hasStatus: true,
		})
	}

	results := make(map[uint64]*ServiceHistoryResult)
	for serviceID, points := range grouped {
		stats := calculateStats(points, period.DownsampleInterval())
		results[serviceID] = &ServiceHistoryResult{
			ServiceID: serviceID,
			Servers: []ServerServiceStats{{
				ServerID: serverID,
				Stats:    stats,
			}},
		}
	}

	return results, nil
}
