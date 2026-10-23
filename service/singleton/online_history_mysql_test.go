package singleton

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
)

// 在线会话生命周期跑在真实 MySQL 上（默认严格 sql_mode）。设置
// NZ_TEST_MYSQL_DSN 才运行，例如：
// root:pass@tcp(127.0.0.1:3306)/nezha?charset=utf8mb4&parseTime=True&loc=Local
func TestOnlineHistoryLifecycleOnMySQL(t *testing.T) {
	dsn := os.Getenv("NZ_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("NZ_TEST_MYSQL_DSN not set")
	}
	previousDB := DB
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	previousConf, previousLoc := Conf, Loc
	Conf = &ConfigClass{Config: &model.Config{}}
	Conf.OnlineStatsDays = model.DefaultOnlineStatsDays
	if Loc == nil {
		Loc = time.UTC
	}
	t.Cleanup(func() { DB, Conf, Loc = previousDB, previousConf, previousLoc })
	require.NoError(t, DB.Migrator().DropTable(&model.OnlineSession{}, &model.Server{}))
	require.NoError(t, DB.AutoMigrate(&model.Server{}, &model.OnlineSession{}))
	require.NoError(t, DB.Exec("INSERT INTO servers (id, name, uuid) VALUES (1, 'server-1', 'online-history-mysql-1')").Error)

	t0 := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	r := NewOnlineHistoryRecorder()
	r.RecordOnline(1, t0)
	sessions, err := r.Sessions(1, t0.Add(-time.Minute), t0.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, sessions, 1, "ongoing session must be persisted")
	require.True(t, sessions[0].Ongoing)
	require.True(t, sessions[0].EndedAt.IsZero(), "unset end time must read back as zero")

	// A fresh recorder restores the ongoing row from the database.
	r2 := NewOnlineHistoryRecorder()
	r2.RecordOnline(1, t0.Add(10*time.Second))
	var count int64
	require.NoError(t, DB.Model(&model.OnlineSession{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "restored session must be continued, not duplicated")

	t1 := t0.Add(20 * time.Second)
	r2.RecordOffline(1, t1)
	sessions, err = r2.Sessions(1, t0.Add(-time.Minute), t1.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.False(t, sessions[0].Ongoing)
	require.WithinDuration(t, t1, sessions[0].EndedAt, time.Second)

	// Orphan cleanup runs the portable NOT IN subquery.
	require.NoError(t, DB.Exec("DELETE FROM servers WHERE id = 1").Error)
	PruneOnlineSessions()
	require.NoError(t, DB.Model(&model.OnlineSession{}).Count(&count).Error)
	require.EqualValues(t, 0, count)
}
