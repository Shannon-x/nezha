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
	if TSDBShared == nil {
		return false
	}
	// 检查 VM 模式的 TSDB 是否已关闭
	if vmDB, ok := TSDBShared.(*tsdb.TSDB); ok {
		return !vmDB.IsClosed()
	}
	return true
}

// CloseTSDB 关闭 TSDB
func CloseTSDB() {
	if TSDBShared != nil {
		TSDBShared.Close()
	}
}

// PerformMaintenance 执行系统维护：按数据库方言收缩存储 + 触发 TSDB retention
//
// 关于不同数据库的压缩策略：
//   - SQLite: VACUUM 重写整个文件，单进程独占
//   - MySQL InnoDB: DELETE 不会归还 .ibd 文件空间，需要 OPTIMIZE TABLE（等价于
//     ALTER TABLE ... FORCE）来重建表回收磁盘。Online DDL 5.6+ 允许并发 DML，
//     但首次跑大表会持有 MDL 较长时间，请放在维护窗口。
//   - PostgreSQL: VACUUM (ANALYZE) 不重写 relation，仅回收死元组到 freespace map；
//     如需把磁盘归还 OS，需要人工跑 VACUUM FULL（持表锁），不在自动化范围。
//   - SQLServer: DBCC SHRINKFILE 风险较高，留给 DBA 手工评估。
var maintenanceTables = []string{"tsdb_service_metrics", "tsdb_server_metrics", "transfers"}

func PerformMaintenance() {
	log.Println("NEZHA>> Starting system maintenance...")

	if DB != nil {
		switch Conf.Database.Type {
		case model.DBTypeSQLite, "":
			runMaintenanceSQL("SQLite VACUUM", "VACUUM")
		case model.DBTypeMySQL:
			for _, t := range maintenanceTables {
				runMaintenanceSQL("MySQL OPTIMIZE "+t, "OPTIMIZE TABLE `"+t+"`")
			}
		case model.DBTypePostgres:
			for _, t := range maintenanceTables {
				runMaintenanceSQL("PG VACUUM "+t, "VACUUM (ANALYZE) "+t)
			}
		}
	}

	if TSDBEnabled() {
		TSDBShared.Maintenance()
	}

	log.Println("NEZHA>> System maintenance completed")
}

func runMaintenanceSQL(label, sql string) {
	log.Printf("NEZHA>> %s: starting...", label)
	if err := DB.Exec(sql).Error; err != nil {
		log.Printf("NEZHA>> %s: failed: %v", label, err)
		return
	}
	log.Printf("NEZHA>> %s: completed", label)
}
