package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/service/singleton"
)

// GHSA-9rc6-8cjv-rcvx：getRedirectURL 构造的 OAuth2 回调 URL 会发给身份提供方，
// 授权码最终落在该地址。若直接信任原始 Host 头，伪造 Host 即可把受害者授权码
// 导向攻击者源、绑定其身份。这些测试钉死回调 Host 的来源策略：仅信任运维声明的
// dashboard host（IsReservedDashboardHost 白名单），否则优先 DashboardHost、其次
// InstallHost，二者皆空才透传请求 Host。

func setRedirectHostConf(t *testing.T, dashboardHost, installHost, reservedHosts string) {
	t.Helper()
	original := singleton.Conf
	t.Cleanup(func() { singleton.Conf = original })
	singleton.Conf = &singleton.ConfigClass{Config: &model.Config{
		ConfigDashboard: model.ConfigDashboard{
			DashboardHost: dashboardHost,
			InstallHost:   installHost,
			ReservedHosts: reservedHosts,
		},
	}}
}

func redirectCtx(host string, forwardedProtoHTTPS bool) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Request.Host = host
	if forwardedProtoHTTPS {
		c.Request.Header.Set("X-Forwarded-Proto", "https")
	}
	return c
}

func TestGetRedirectURL_ForgedHostPinnedToDashboardHost(t *testing.T) {
	setRedirectHostConf(t, "dash.example.com", "install.example.com", "")
	got := getRedirectURL(redirectCtx("evil.attacker.com", false))
	require.Equal(t, "http://dash.example.com/api/v1/oauth2/callback", got,
		"伪造 Host 必须被忽略，回调锁定到 DashboardHost")
}

func TestGetRedirectURL_ForgedHostFallsBackToInstallHostWhenDashboardHostEmpty(t *testing.T) {
	setRedirectHostConf(t, "", "install.example.com", "")
	got := getRedirectURL(redirectCtx("evil.attacker.com", false))
	require.Equal(t, "http://install.example.com/api/v1/oauth2/callback", got,
		"未配置 DashboardHost 时，伪造 Host 应回退到 InstallHost（强于上游默认的透传）")
}

func TestGetRedirectURL_TrustsReservedDashboardHostVerbatim(t *testing.T) {
	setRedirectHostConf(t, "dash.example.com", "panel.example.com", "")
	// 请求 Host 命中 InstallHost（保留 host 白名单成员）→ 原样透传，不被重写。
	got := getRedirectURL(redirectCtx("panel.example.com", false))
	require.Equal(t, "http://panel.example.com/api/v1/oauth2/callback", got,
		"运维声明的 dashboard host 必须被原样信任")
}

func TestGetRedirectURL_TrustsReservedHostForMultiDomain(t *testing.T) {
	setRedirectHostConf(t, "", "install.example.com", "extra.example.com")
	got := getRedirectURL(redirectCtx("extra.example.com", false))
	require.Equal(t, "http://extra.example.com/api/v1/oauth2/callback", got,
		"ReservedHosts 中声明的多域名同样被信任、原样透传")
}

func TestGetRedirectURL_BothEmptyPassesThroughRequestHost(t *testing.T) {
	setRedirectHostConf(t, "", "", "")
	got := getRedirectURL(redirectCtx("anything.example.com", false))
	require.Equal(t, "http://anything.example.com/api/v1/oauth2/callback", got,
		"DashboardHost 与 InstallHost 都未配置时才透传请求 Host")
}

func TestGetRedirectURL_HonoursForwardedProtoOnTrustedHost(t *testing.T) {
	setRedirectHostConf(t, "", "panel.example.com", "")
	got := getRedirectURL(redirectCtx("panel.example.com", true))
	require.Equal(t, "https://panel.example.com/api/v1/oauth2/callback", got,
		"X-Forwarded-Proto=https 在受信任 Host 上应产生 https 回调")
}

func TestGetRedirectURL_ForgedHostCannotForceOrigin(t *testing.T) {
	setRedirectHostConf(t, "dash.example.com", "install.example.com", "")
	// 伪造 Host 即便带 https 前向代理头，源仍被锁定到 DashboardHost，scheme 不影响 host。
	got := getRedirectURL(redirectCtx("evil.attacker.com", true))
	require.Equal(t, "https://dash.example.com/api/v1/oauth2/callback", got,
		"伪造 Host 无法操纵回调源，仅 scheme 受前向代理头影响")
}
