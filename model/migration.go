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

// NeedsServiceHideForGuestMigration 判断是否需要把旧的 enable_show_in_service
// 可见性迁移到 hide_for_guest。上游将 Service.EnableShowInService（true=公开）
// 改为 HideForGuest（true=对游客隐藏）且未做数据迁移，直接升级会让原本不公开
// 的服务全部对游客可见。必须在 AutoMigrate 之前调用：之后 hide_for_guest 列
// 已被创建，无法再区分“首次升级”与“已迁移过”。
func NeedsServiceHideForGuestMigration(db *gorm.DB) bool {
	m := db.Migrator()
	return m.HasTable(&Service{}) &&
		m.HasColumn(&Service{}, "enable_show_in_service") &&
		!m.HasColumn(&Service{}, "hide_for_guest")
}

// MigrateServiceHideForGuest 在 AutoMigrate 创建 hide_for_guest 列之后，把原先
// 未公开（enable_show_in_service 为 false 或 NULL）的服务标记为对游客隐藏，
// 其余服务显式置 false，避免新列残留 NULL。旧列保留不删，便于回滚。
func MigrateServiceHideForGuest(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		hidden := tx.Model(&Service{}).
			Where("enable_show_in_service = ? OR enable_show_in_service IS NULL", false).
			UpdateColumn("hide_for_guest", true)
		if hidden.Error != nil {
			return hidden.Error
		}
		if err := tx.Model(&Service{}).
			Where("hide_for_guest IS NULL").
			UpdateColumn("hide_for_guest", false).Error; err != nil {
			return err
		}
		log.Printf("NEZHA>> Migration: marked %d non-public services as hide_for_guest", hidden.RowsAffected)
		return nil
	})
}
