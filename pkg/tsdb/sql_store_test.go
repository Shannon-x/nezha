package tsdb

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSQLStoreQueriesSanitizeStoredNonFiniteSamples(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "metrics.db")), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	store, err := OpenSQL(db, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	today := time.Now().UTC().Truncate(24 * time.Hour)
	for i, sample := range []float64{math.Inf(1), math.Inf(-1), 18} {
		ts := today.Add(-time.Hour + time.Duration(i)*time.Second)
		require.NoError(t, db.Create(&TSDBServerMetric{
			ServerID: 1, MetricName: string(MetricServerCPU), Value: sample, CreatedAt: ts,
		}).Error)
		require.NoError(t, db.Create(&TSDBServiceMetric{
			ServiceID: 2, ServerID: 1, Delay: sample, Status: 1, CreatedAt: ts,
		}).Error)
	}

	cpu, err := store.QueryServerMetrics(1, MetricServerCPU, Period7Days)
	require.NoError(t, err)
	require.Len(t, cpu, 1)
	require.Equal(t, 6.0, cpu[0].Value)

	byService, err := store.QueryServiceHistory(2, Period7Days)
	require.NoError(t, err)
	byServer, err := store.QueryServiceHistoryByServerID(1, Period7Days)
	require.NoError(t, err)
	require.Contains(t, byServer, uint64(2))
	for _, history := range []*ServiceHistoryResult{byService, byServer[2]} {
		require.Len(t, history.Servers, 1)
		stats := history.Servers[0].Stats
		require.Equal(t, uint64(3), stats.TotalUp)
		require.Equal(t, 6.0, stats.AvgDelay)
		require.Len(t, stats.DataPoints, 1)
		require.Equal(t, 6.0, stats.DataPoints[0].Delay)
	}

	daily, err := store.QueryServiceDailyStats(2, today, 2)
	require.NoError(t, err)
	batch, err := store.QueryServicesDailyStats([]uint64{2}, today, 2)
	require.NoError(t, err)
	want := []DailyServiceStats{{Up: 3, Delay: 6}, {}}
	require.Equal(t, want, daily)
	require.Equal(t, want, batch[2])
}
