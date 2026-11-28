package backup

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func otherFilesystem(t *testing.T, srcDir string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/dev/shm", "nezha-restore-")
	if err != nil {
		t.Skip("second writable filesystem unavailable")
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	probe := filepath.Join(srcDir, "probe")
	require.NoError(t, os.WriteFile(probe, nil, 0600))
	err = os.Rename(probe, filepath.Join(dir, "probe"))
	if !errors.Is(err, syscall.EXDEV) {
		t.Skip("test requires distinct mounts")
	}
	return dir
}

func TestCrossDeviceMoveKeepsSourceUntilDestinationIsDurable(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := otherFilesystem(t, srcDir)
	src, dst := filepath.Join(srcDir, "live.db"), filepath.Join(dstDir, "kept.db")
	require.NoError(t, os.WriteFile(src, []byte("original"), 0600))
	syncErr := errors.New("destination sync failed")
	bootFault = func(op string) error {
		if op == "sync "+dstDir {
			return syncErr
		}
		return nil
	}
	t.Cleanup(func() { bootFault = nil })
	require.ErrorIs(t, moveFile(src, dst), syncErr)
	original, err := os.ReadFile(src)
	require.NoError(t, err)
	require.Equal(t, "original", string(original))

	bootFault = nil
	require.NoError(t, moveFile(src, dst))
	require.NoFileExists(t, src)
	moved, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, original, moved)
}

func TestRollbackAfterCrossDeviceSourceCannotBeRemoved(t *testing.T) {
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("requires POSIX directory permissions")
	}
	srcDir := t.TempDir()
	dataDir := otherFilesystem(t, srcDir)
	rollbackDir := filepath.Join(dataDir, rollbackDirName)
	require.NoError(t, os.Mkdir(rollbackDir, 0700))
	src, kept := filepath.Join(srcDir, "live.db"), filepath.Join(rollbackDir, "kept.db")
	require.NoError(t, os.WriteFile(src, []byte("original"), 0600))
	require.NoError(t, os.Chmod(srcDir, 0500))
	t.Cleanup(func() { _ = os.Chmod(srcDir, 0700) })
	require.Error(t, moveFile(src, kept))
	require.FileExists(t, kept)
	boot := &Boot{dataDir: dataDir, state: &restoreState{Steps: []swapStep{{Path: src, Rollback: kept}}}}

	// A different live file still needs to be restored. Keep the journal
	// and the rollback copy when the directory cannot be changed.
	require.NoError(t, os.WriteFile(src, []byte("modified"), 0600))
	require.Error(t, boot.rollback(errors.New("swap failed")))
	require.True(t, boot.Applied())
	require.FileExists(t, kept)

	require.NoError(t, os.WriteFile(src, []byte("original"), 0600))
	require.NoError(t, boot.rollback(errors.New("swap failed")))
	require.False(t, boot.Applied())
	require.NoFileExists(t, kept)
	got, err := os.ReadFile(src)
	require.NoError(t, err)
	require.Equal(t, "original", string(got))
}
