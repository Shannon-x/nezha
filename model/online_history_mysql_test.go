package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// MySQL 严格模式拒绝零值日期：进行中会话的 EndedAt 落库前换成占位时间，
// 读出（含驱动按本地时区换算后）还原为零值；其他方言不受影响。
func TestOnlineSessionUnsetTimesOnMySQL(t *testing.T) {
	started := time.Date(2026, 10, 23, 10, 25, 32, 0, time.UTC)
	mysqlTx := &gorm.DB{Config: &gorm.Config{Dialector: mysql.New(mysql.Config{})}}

	s := OnlineSession{StartedAt: started, LastActiveAt: started, Ongoing: true}
	require.NoError(t, s.BeforeSave(mysqlTx))
	require.Equal(t, onlineSessionUnsetTime, s.EndedAt)
	require.Equal(t, started, s.StartedAt)

	// 驱动以 loc=Local 读回时可能带时区偏移
	s.EndedAt = onlineSessionUnsetTime.In(time.FixedZone("UTC+8", 8*3600))
	require.NoError(t, s.AfterFind(nil))
	require.True(t, s.EndedAt.IsZero())
	require.Equal(t, started, s.StartedAt)
	require.Equal(t, started, s.LastActiveAt)

	other := OnlineSession{StartedAt: started}
	require.NoError(t, other.BeforeSave(nil))
	require.True(t, other.EndedAt.IsZero(), "non-MySQL dialects keep the zero value")
}
