package singleton

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/nezhahq/nezha/model"
)

func TestBootstrapAdminPassword(t *testing.T) {
	t.Setenv("NZ_ADMIN_PASSWORD", "")
	a, generated, err := BootstrapAdminPassword()
	require.NoError(t, err)
	require.True(t, generated)
	require.Len(t, a, 20)
	b, _, err := BootstrapAdminPassword()
	require.NoError(t, err)
	require.NotEqual(t, a, b)

	t.Setenv("NZ_ADMIN_PASSWORD", "from-env-secret")
	p, generated, err := BootstrapAdminPassword()
	require.NoError(t, err)
	require.False(t, generated)
	require.Equal(t, "from-env-secret", p)
}

func TestRunSecurityAuditFlagsRisks(t *testing.T) {
	previousDB, previousConf := DB, Conf
	t.Cleanup(func() { DB, Conf = previousDB, previousConf })
	var err error
	DB, err = gorm.Open(openSQLiteDialector(filepath.Join(t.TempDir(), "audit.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&model.User{}, &model.APIToken{}, &model.Cron{}, &model.NAT{}))
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.MinCost)
	require.NoError(t, err)
	require.NoError(t, DB.Create(&model.User{Username: "admin", Password: string(hash)}).Error)

	Conf = &ConfigClass{Config: &model.Config{JWTSecretKey: "short", AgentSecretKey: "also-short"}}
	Conf.Security.DisableTerminal = true

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("listen_port: 8008\n"), 0o644))

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	RunSecurityAudit(configPath)
	out := buf.String()

	st, err := os.Stat(configPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	require.Contains(t, out, "用户 admin 的密码是弱口令")
	require.Contains(t, out, "jwt_secret_key 长度不足")
	require.Contains(t, out, "agent_secret_key 长度不足")
	require.Contains(t, out, "已关闭：disable_terminal")
	require.Contains(t, out, "用户 1 个 [admin(admin)]")
}

func TestFrontendLockFallsBackToBuiltin(t *testing.T) {
	require.NoError(t, InitFrontendTemplates())
	previousConf := Conf
	t.Cleanup(func() { Conf = previousConf })
	Conf = &ConfigClass{Config: &model.Config{}}
	Conf.UserTemplate, Conf.AdminTemplate = "nazhua-dist", "frontends/evil/admin"

	user, admin := Conf.FrontendSelection()
	require.Equal(t, "frontends/evil/admin", admin, "unlocked selection is served as configured")

	Conf.Security.DisableFrontendInstall = true
	user, admin = Conf.FrontendSelection()
	require.Equal(t, "nazhua-dist", user, "built-in user frontends stay selectable")
	require.Equal(t, "admin-dist", admin, "third-party admin frontend falls back to the built-in one")

	Conf.UserTemplate = "frontends/evil/user"
	user, _ = Conf.FrontendSelection()
	require.Equal(t, "user-dist", user)
	// The settings page must round-trip the effective selection, or saving fails validation.
	snapshot := Conf.SettingsSnapshot()
	require.Equal(t, "user-dist", snapshot.UserTemplate)
	require.Equal(t, "admin-dist", snapshot.AdminTemplate)
	for _, tpl := range Conf.AvailableFrontendTemplates() {
		require.NotContains(t, tpl.Path, "frontends/", "only built-in templates are offered while locked")
	}
}
