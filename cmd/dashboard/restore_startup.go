package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/backup"
	"github.com/nezhahq/nezha/service/singleton"
)

func (p DashboardCliParam) bootRestore() (*backup.Boot, error) {
	return backup.BootRestoreIn(backup.DashboardDataDir(p.ConfigFile), p.ConfigFile, p.DatabaseLocation, p.restoreDatabasePath)
}

// restoreDatabasePath checks the operator's live database and capability
// policy before a restored configuration can take effect.
func (p DashboardCliParam) restoreDatabasePath(stagedConfig string) (string, error) {
	read := func(path string) (string, model.SecurityConf, error) {
		cfg, security, err := model.ReadRestoreSettings(path)
		if err != nil {
			return "", security, err
		}
		db := p.databaseConfig(cfg)
		file := backup.SQLiteFilePath(db.Path)
		if db.Type != model.DBTypeSQLite || file == "" {
			return "", security, errors.New("restore: backups require a file-backed SQLite database")
		}
		file, err = backup.ResolveSQLitePath(file)
		return file, security, err
	}
	live, security, err := read(p.ConfigFile)
	if err != nil || stagedConfig == "" {
		return live, err
	}
	restored, restoredSecurity, err := read(stagedConfig)
	if err != nil {
		return "", err
	}
	if live != restored {
		return "", errors.New("restore: the archived configuration selects a different database")
	}
	if security != restoredSecurity {
		return "", errors.New("restore: the archived configuration changes the dashboard security policy")
	}
	return live, nil
}

// restoreCommitter is implemented by *backup.Boot (nil-safe).
type restoreCommitter interface {
	Commit() error
	Rollback(cause error)
}

// finishRestoreStartup commits a restore applied during this start only
// once the dashboard is ready to serve. If startup failed (configuration,
// database, listeners, TLS of the restored data), the previous data and
// configuration are put back before the process exits, so the supervisor
// starts the dashboard with them again. If the commit itself cannot be
// recorded, the dashboard must not serve either: the restore stays planned
// and the next start rolls it back.
func finishRestoreStartup(boot restoreCommitter, startupErr error) error {
	if startupErr == nil {
		if err := boot.Commit(); err != nil {
			closeDatabase()
			return err
		}
		return nil
	}
	closeDatabase()
	boot.Rollback(startupErr)
	return startupErr
}

func closeDatabase() {
	if singleton.DB != nil {
		if sqlDB, err := singleton.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// openDashboardHTTPS opens the listener and loads the certificate the HTTPS
// server will use, before a restore is committed. The listener comes from
// the same selection as every dashboard listener (an inherited socket in
// builds that adopt one, otherwise a bind), and the server later serves on
// exactly this listener with exactly this certificate, so nothing that was
// checked can change in between. It returns nil when HTTPS is disabled.
func openDashboardHTTPS(address string, port uint16, certPath, keyPath string) (net.Listener, *tls.Certificate, error) {
	if port == 0 {
		return nil, nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("https: load certificate: %w", err)
	}
	l, err := openDashboardListener("tcp", address, dashboardHTTPSListener)
	if err != nil {
		return nil, nil, fmt.Errorf("https: %w", err)
	}
	return l, &cert, nil
}
