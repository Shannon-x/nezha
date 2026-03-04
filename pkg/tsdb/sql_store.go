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

	var records []TSDBServiceMetric
	if err := s.db.Where("service_id = ? AND created_at >= ?", serviceID, since).
		Order("server_id, created_at").Find(&records).Error; err != nil {
		return nil, err
	}

	result := &ServiceHistoryResult{
		ServiceID: serviceID,
		Servers:   make([]ServerServiceStats, 0),
	}

	// 按 server_id 分组
	grouped := make(map[uint64][]rawDataPoint)
	for _, r := range records {
		grouped[r.ServerID] = append(grouped[r.ServerID], rawDataPoint{
			timestamp: r.CreatedAt.UnixMilli(),
			value:     r.Delay,
			status:    float64(r.Status),
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

	var records []TSDBServiceMetric
	if err := s.db.Where("service_id = ? AND created_at >= ? AND created_at < ?", serviceID, start, today).
		Find(&records).Error; err != nil {
		return nil, err
	}

	delayCount := make([]int, days)
	for _, r := range records {
		dayIndex := (days - 1) - int(today.Sub(r.CreatedAt).Hours())/24
		if dayIndex < 0 || dayIndex >= days {
			continue
		}
		if r.Status >= 1 {
			stats[dayIndex].Up++
		} else {
			stats[dayIndex].Down++
		}
		stats[dayIndex].Delay = (stats[dayIndex].Delay*float64(delayCount[dayIndex]) + r.Delay) / float64(delayCount[dayIndex]+1)
		delayCount[dayIndex]++
	}

	return stats, nil
}

// QueryServerMetrics 查询服务器指标历史
func (s *SQLStore) QueryServerMetrics(serverID uint64, metric MetricType, period QueryPeriod) ([]MetricDataPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("SQLStore is closed")
	}

	since := time.Now().Add(-period.Duration())

	var records []TSDBServerMetric
	if err := s.db.Where("server_id = ? AND metric_name = ? AND created_at >= ?", serverID, string(metric), since).
		Order("created_at").Find(&records).Error; err != nil {
		return nil, err
	}

	points := make([]rawDataPoint, 0, len(records))
	for _, r := range records {
		points = append(points, rawDataPoint{
			timestamp: r.CreatedAt.UnixMilli(),
			value:     r.Value,
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

	var records []TSDBServiceMetric
	if err := s.db.Where("server_id = ? AND created_at >= ?", serverID, since).
		Order("service_id, created_at").Find(&records).Error; err != nil {
		return nil, err
	}

	// 按 service_id 分组
	grouped := make(map[uint64][]rawDataPoint)
	for _, r := range records {
		grouped[r.ServiceID] = append(grouped[r.ServiceID], rawDataPoint{
			timestamp: r.CreatedAt.UnixMilli(),
			value:     r.Delay,
			status:    float64(r.Status),
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
