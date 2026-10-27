package model

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	pb "github.com/nezhahq/nezha/proto"
)

func allSecurityDisabled() SecurityConf {
	return SecurityConf{
		DisableCommandExecute: true, DisableTerminal: true, DisableFileManager: true, DisableAgentConfig: true,
		DisableServerTransfer: true, DisableAgentUpgrade: true, DisableNAT: true,
	}
}

func TestSecurityPolicyBlocksOnlyConfiguredTaskTypes(t *testing.T) {
	t.Cleanup(func() { ApplySecurityPolicy(SecurityConf{}) })
	ApplySecurityPolicy(SecurityConf{DisableTerminal: true})

	var disabled *TaskDisabledError
	require.ErrorAs(t, CheckTaskAllowed(TaskTypeTerminalGRPC), &disabled)
	require.Equal(t, "disable_terminal", disabled.Key)
	require.NoError(t, CheckTaskAllowed(TaskTypeCommand))
	require.NoError(t, CheckTaskAllowed(TaskTypeHTTPGet))

	// The gate runs before the stream lookup, so even an offline agent reports the policy error.
	s := &Server{}
	require.ErrorAs(t, s.SendTask(&pb.Task{Type: TaskTypeTerminalGRPC}), &disabled)
	require.True(t, errors.Is(s.SendTask(&pb.Task{Type: TaskTypeHTTPGet}), ErrTaskStreamOffline))

	// Every delivery entry point is gated, including the queued fan-out used by cron.
	require.ErrorAs(t, s.SendTaskGuarded(&pb.Task{Type: TaskTypeTerminalGRPC}, nil), &disabled)
	require.ErrorAs(t, s.QueueTaskGuarded(&pb.Task{Type: TaskTypeTerminalGRPC}, nil), &disabled)
	require.ErrorAs(t, s.QueueTask(&pb.Task{Type: TaskTypeTerminalGRPC}), &disabled)
}

// Every task type that is not a passive monitor or keepalive grants control over the
// agent. If upstream adds a new one, this fails until it is mapped to a security switch.
func TestSecurityPolicyCoversEveryPrivilegedTaskType(t *testing.T) {
	t.Cleanup(func() { ApplySecurityPolicy(SecurityConf{}) })
	ApplySecurityPolicy(allSecurityDisabled())
	for tt := uint64(TaskTypeHTTPGet); tt <= TaskTypeFsTransfer; tt++ {
		if IsServiceMonitorType(tt) || tt == TaskTypeKeepalive || tt == TaskTypeReportHostInfoDeprecated {
			require.NoError(t, CheckTaskAllowed(tt), "task type %d must stay allowed", tt)
			continue
		}
		require.Error(t, CheckTaskAllowed(tt), "privileged task type %d is not covered by any security switch", tt)
	}
}

func TestSecurityConfLoadsFromFileAndEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("security:\n  disable_terminal: true\n  disable_file_manager: true\n"), 0o600))
	t.Setenv("NZ_SECURITY_DISABLECOMMANDEXECUTE", "true")

	var c Config
	require.NoError(t, c.Read(path, nil))
	require.True(t, c.Security.DisableTerminal)
	require.True(t, c.Security.DisableFileManager)
	require.True(t, c.Security.DisableCommandExecute)
	require.False(t, c.Security.DisableNAT)

	// Saving the config (as the settings API does) keeps the switches.
	require.NoError(t, c.Save())
	var reloaded Config
	require.NoError(t, reloaded.Read(path, nil))
	require.True(t, reloaded.Security.DisableTerminal)
	require.True(t, reloaded.Security.DisableFileManager)
}
