package singleton

import (
	"context"
	"log"
	"path/filepath"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/backup"
)

// BackupShared runs encrypted backups to S3 compatible storage and staged
// restores. It requires a file-backed SQLite database and valid settings.
var BackupShared *backup.Manager

// RequestRestart asks the dashboard to shut down gracefully; its supervisor
// (Docker restart policy, systemd Restart=) starts it again. main sets it.
var RequestRestart func()

// InitBackup starts the backup manager. Backups are optional: a failure is
// logged and leaves the feature unavailable instead of stopping the dashboard.
func InitBackup(configPath, dbDSN string) error {
	dbPath := backup.SQLiteFilePath(dbDSN)
	if dbPath == "" || DB == nil || DB.Dialector.Name() != model.DBTypeSQLite {
		log.Println("NEZHA>> backup: disabled, backups require a file-backed SQLite database")
		return nil
	}
	dataDir := backup.DashboardDataDir(configPath)
	m, err := backup.NewManager(backup.Options{
		DataDir:      dataDir,
		ConfigPath:   configPath,
		DatabasePath: dbPath,
		SettingsPath: filepath.Join(dataDir, backup.SettingsFileName),
		Version:      Version,
		Snapshot:     snapshotDatabase,
		CronParser:   newCronSpecParser(),
		Location:     Loc,
		Restart: func() {
			if RequestRestart != nil {
				RequestRestart()
			}
		},
	})
	if err != nil {
		log.Printf("NEZHA>> backup: disabled: %v", err)
		return nil
	}
	BackupShared = m
	return nil
}

// snapshotDatabase writes a consistent copy of the live database to dst.
// VACUUM INTO runs in one read transaction, so the copy is a single point in
// time and the dashboard keeps running; writers wait for it like for any
// other reader.
func snapshotDatabase(ctx context.Context, dst string) error {
	return DB.WithContext(ctx).Exec("VACUUM INTO ?", dst).Error
}
