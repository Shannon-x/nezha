package singleton

import (
	"log"
	"time"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/tsdb"
)

// TSDBShared 全局 TSDB 存储实例（Store 接口）
var TSDBShared tsdb.Store

// InitTSDB 初始化 TSDB
// 策略：
//   - tsdb.type = "vm" 且 data_path 非空 → VictoriaMetrics 模式
//   - tsdb.type = "sql" 或未配置（默认） → SQL 模式（自动使用已连接的数据库）
//   - 这意味着只要配置了 NZ_DATABASE_TYPE=mysql，TSDB 会自动存储到 MySQL
func InitTSDB() error {
	tsdbType := Conf.TSDB.Type

	// 构建通用配置
	config := &tsdb.Config{
		RetentionDays:      30,
		MinFreeDiskSpaceGB: 1,
		MaxMemoryMB:        256,
	}
	if Conf.TSDB.RetentionDays > 0 {
		config.RetentionDays = Conf.TSDB.RetentionDays
	}
	if Conf.TSDB.WriteBufferSize > 0 {
		config.WriteBufferSize = Conf.TSDB.WriteBufferSize
	}
	if Conf.TSDB.WriteBufferFlushInterval > 0 {
		config.WriteBufferFlushInterval = time.Duration(Conf.TSDB.WriteBufferFlushInterval) * time.Second
	}

	// VictoriaMetrics 模式：仅当明确配置 type=vm 且 data_path 非空时
	if tsdbType == model.TSDBTypeVM && Conf.TSDB.DataPath != "" {
		return initVMStore(config)
	}

	// SQL 模式（默认）：自动使用已连接的数据库
	if DB != nil {
		return initSQLStore(config)
	}

	// 数据库都没连接，禁用 TSDB
	log.Println("NEZHA>> TSDB is disabled (no database connection)")
	return nil
}

// initSQLStore 初始化 SQL 模式的 TSDB
func initSQLStore(config *tsdb.Config) error {
	var err error
	TSDBShared, err = tsdb.OpenSQL(DB, config)
	if err != nil {
		return err
	}
	log.Println("NEZHA>> TSDB initialized in SQL mode (data stored in your database)")
	return nil
}

// initVMStore 初始化 VictoriaMetrics 模式的 TSDB
func initVMStore(config *tsdb.Config) error {
	config.DataPath = Conf.TSDB.DataPath
	if Conf.TSDB.MinFreeDiskSpaceGB > 0 {
		config.MinFreeDiskSpaceGB = Conf.TSDB.MinFreeDiskSpaceGB
	}
	if Conf.TSDB.MaxMemoryMB > 0 {
		config.MaxMemoryMB = Conf.TSDB.MaxMemoryMB
	}

	var err error
	TSDBShared, err = tsdb.Open(config)
	if err != nil {
		return err
	}
	log.Println("NEZHA>> TSDB initialized in VictoriaMetrics mode")
	return nil
}

// TSDBEnabled 检查 TSDB 是否启用
func TSDBEnabled() bool {
	return TSDBShared != nil
}

// CloseTSDB 关闭 TSDB
func CloseTSDB() {
	if TSDBShared != nil {
		if err := TSDBShared.Close(); err != nil {
			log.Printf("NEZHA>> Warning: failed to close TSDB: %v", err)
		}
	}
}

// PerformMaintenance 执行系统存储维护
// 对 SQLite 执行 VACUUM，对 TSDB 执行清理/刷盘
func PerformMaintenance() {
	log.Println("NEZHA>> Starting system maintenance...")

	// 仅对 SQLite 执行 VACUUM
	if Conf.Database.Type == model.DBTypeSQLite || Conf.Database.Type == "" {
		if DB != nil {
			if err := DB.Exec("VACUUM").Error; err != nil {
				log.Printf("NEZHA>> Warning: VACUUM failed: %v", err)
			} else {
				log.Println("NEZHA>> SQLite VACUUM completed")
			}
		}
	}

	// TSDB 维护（VM模式刷盘，SQL模式清理过期数据）
	if TSDBEnabled() {
		TSDBShared.Maintenance()
	}

	log.Println("NEZHA>> System maintenance completed")
}
