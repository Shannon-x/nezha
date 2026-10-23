package singleton

import (
	"crypto/rand"
	"log"
	"math/big"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/nezhahq/nezha/model"
)

// fork 增加的安全加固：初始管理员密码与启动安全自检。

const auditPrefix = "NEZHA>> [安全自检] "

// BootstrapAdminPassword 返回空库初始化管理员时使用的密码。上游固定为
// admin/admin，面板在改密前一旦暴露公网，任何人都能登录并对所有 agent 下发
// 命令。这里优先使用环境变量 NZ_ADMIN_PASSWORD，否则生成随机密码。
func BootstrapAdminPassword() (password string, generated bool, err error) {
	if p := os.Getenv("NZ_ADMIN_PASSWORD"); p != "" {
		return p, false, nil
	}
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	var b strings.Builder
	for i := 0; i < 20; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", false, err
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String(), true, nil
}

// RunSecurityAudit 在启动时检查常见的高危配置并列出安全相关清单，便于发现
// 被入侵后残留的账号、令牌与计划任务。只读取与输出日志，配置文件权限过宽时
// 会收紧为 0600。
func RunSecurityAudit(configPath string) {
	if Conf == nil || DB == nil {
		return
	}
	warn := func(format string, args ...any) { log.Printf(auditPrefix+"警告："+format, args...) }
	info := func(format string, args ...any) { log.Printf(auditPrefix+format, args...) }

	// 配置文件含 JWT 密钥、agent 密钥与数据库密码
	if st, err := os.Stat(configPath); err == nil && st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(configPath, 0o600); err != nil {
			warn("配置文件 %s 权限为 %v，其他用户可读，且自动收紧失败：%v", configPath, st.Mode().Perm(), err)
		} else {
			info("配置文件 %s 权限已从 %v 收紧为 0600", configPath, st.Mode().Perm())
		}
	}

	if len(Conf.JWTSecretKey) < 32 {
		warn("jwt_secret_key 长度不足 32 位，容易被猜解伪造登录态，请删除该字段让面板重新生成")
	}
	if len(Conf.AgentSecretKey) < 32 {
		warn("agent_secret_key 长度不足 32 位，请更换为随机长密钥并同步更新各 agent")
	}
	if len(Conf.Oauth2) > 0 && Conf.DashboardHost == "" {
		warn("已配置 OAuth2 但未设置 dashboard_host，请设置为面板对外域名，防止回调地址被 Host 头篡改")
	}
	if Conf.HTTPS.ListenPort == 0 {
		info("面板未直接启用 HTTPS：请确认只通过 HTTPS 反向代理访问，且 agent 配置 tls: true，否则密码、登录态和下发给 agent 的指令都是明文传输")
	}

	disabled, enabled := Conf.Security.SecurityPolicySummary()
	if len(enabled) > 0 {
		info("仍开启的高危能力：%s。不使用的请在 config.yaml 的 security 段关闭（已关闭：%s）",
			strings.Join(enabled, ", "), strings.Join(append([]string{}, disabled...), ", "))
	} else {
		info("面板侧高危能力已全部关闭")
	}

	var users []model.User
	if err := DB.Order("id").Find(&users).Error; err != nil {
		warn("读取用户列表失败：%v", err)
		return
	}
	names := make([]string, 0, len(users))
	for i, u := range users {
		role := "member"
		if u.Role.IsAdmin() {
			role = "admin"
		}
		names = append(names, u.Username+"("+role+")")
		// bcrypt 校验较慢，只检查前 50 个账号
		if i < 50 && !u.RejectPassword {
			for _, weak := range []string{"admin", u.Username} {
				if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(weak)) == nil {
					warn("用户 %s 的密码是弱口令（%s），请立即修改", u.Username, weak)
					break
				}
			}
		}
	}

	var tokens []model.APIToken
	DB.Find(&tokens)
	noExpiry := 0
	for _, t := range tokens {
		if t.ExpiresAt == nil {
			noExpiry++
		}
	}
	var crons, cmdCrons, nats int64
	DB.Model(&model.Cron{}).Count(&crons)
	DB.Model(&model.Cron{}).Where("command <> ?", "").Count(&cmdCrons)
	DB.Model(&model.NAT{}).Count(&nats)

	info("清单：用户 %d 个 [%s]；API Token %d 个（其中 %d 个永不过期）；计划任务 %d 个（含命令 %d 个）；内网穿透 %d 个",
		len(users), strings.Join(names, ", "), len(tokens), noExpiry, crons, cmdCrons, nats)
	info("如果其中有你不认识的用户、Token、计划任务或内网穿透，面板可能已被入侵：请立即删除，并轮换 jwt_secret_key、agent_secret_key 和所有密码")
}
