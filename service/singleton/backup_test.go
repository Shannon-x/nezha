package singleton

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

func TestInitBackupRequiresFileBackedSQLite(t *testing.T) {
	oldDB, oldBackup := DB, BackupShared
	t.Cleanup(func() { DB, BackupShared = oldDB, oldBackup })
	path := filepath.Join(t.TempDir(), "custom.db")
	config := filepath.Join(t.TempDir(), "config.yaml")
	for _, tc := range []struct {
		dialect gorm.Dialector
		path    string
		enabled bool
	}{
		{mysql.Open(""), path, false},
		{postgres.Open(""), path, false},
		{sqlserver.Open(""), path, false},
		{openSQLiteDialector(":memory:"), ":memory:", false},
		{openSQLiteDialector(path), path, true},
	} {
		DB = &gorm.DB{Config: &gorm.Config{Dialector: tc.dialect}}
		BackupShared = nil
		require.NoError(t, InitBackup(config, tc.path))
		if BackupShared != nil {
			t.Cleanup(BackupShared.Close)
		}
		require.Equal(t, tc.enabled, BackupShared != nil, "%s: %s", tc.dialect.Name(), tc.path)
	}
}
