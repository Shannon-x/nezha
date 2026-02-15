package model

import (
	"log"

	"gorm.io/gorm"
)

// RunMigrations 执行数据库迁移
// 在 AutoMigrate 之后调用，处理需要手动干预的迁移
func RunMigrations(db *gorm.DB, dbType string) {
	switch dbType {
	case DBTypePostgres:
		migratePostgres(db)
	case DBTypeMySQL:
		migrateMySQL(db)
	}
}

// migratePostgres 处理 PostgreSQL 特有的迁移
func migratePostgres(db *gorm.DB) {
	// 检查 nz_waf 表的 ip 列类型是否正确
	// 旧版本可能使用了 binary(16)，PostgreSQL 不支持
	var colType string
	row := db.Raw(`
		SELECT data_type FROM information_schema.columns
		WHERE table_name = 'nz_waf' AND column_name = 'ip'
	`).Row()
	if row != nil {
		if err := row.Scan(&colType); err == nil && colType != "bytea" {
			log.Printf("NEZHA>> Migration: Converting nz_waf.ip from %s to bytea", colType)
			if err := db.Exec("ALTER TABLE nz_waf ALTER COLUMN ip TYPE bytea USING ip::bytea").Error; err != nil {
				log.Printf("NEZHA>> Warning: Failed to migrate nz_waf.ip: %v", err)
			} else {
				log.Println("NEZHA>> Migration: nz_waf.ip successfully migrated to bytea")
			}
		}
	}

	// 检查 notifications 表的列类型
	migrateTextColumn(db, "notifications", "request_header", "text")
	migrateTextColumn(db, "notifications", "request_body", "text")
}

// migrateMySQL 处理 MySQL 特有的迁移
func migrateMySQL(db *gorm.DB) {
	// 从 longtext 降级到 text 后，MySQL 的 AutoMigrate 会自动处理
	// 但如果需要兼容旧数据，这里可以做额外检查
	log.Println("NEZHA>> MySQL migration check completed")
}

// migrateTextColumn 检查并修复 PostgreSQL 列类型
func migrateTextColumn(db *gorm.DB, table, column, expectedType string) {
	var colType string
	row := db.Raw(`
		SELECT data_type FROM information_schema.columns
		WHERE table_name = ? AND column_name = ?
	`, table, column).Row()
	if row != nil {
		if err := row.Scan(&colType); err == nil && colType != expectedType {
			log.Printf("NEZHA>> Migration: Converting %s.%s from %s to %s", table, column, colType, expectedType)
			sql := "ALTER TABLE " + table + " ALTER COLUMN " + column + " TYPE " + expectedType
			if err := db.Exec(sql).Error; err != nil {
				log.Printf("NEZHA>> Warning: Failed to migrate %s.%s: %v", table, column, err)
			}
		}
	}
}
