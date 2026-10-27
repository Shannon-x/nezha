package model

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// SecurityConf 是面板侧的高危能力锁（fork 增加）。agent 以 root/SYSTEM 运行，
// 面板一旦被攻破（弱口令、泄露的 JWT/API Token、面板漏洞），攻击者就能通过
// 下发任务控制所有服务器。这些开关只能在 config.yaml（或环境变量）中设置，
// 设置接口无法修改，因此即使面板账号失守，被关闭的能力也无法被重新打开。
//
// 环境变量写法：NZ_SECURITY_DISABLECOMMANDEXECUTE=true（键名去掉下划线）。
type SecurityConf struct {
	// 批量/计划任务执行命令、MCP exec
	DisableCommandExecute bool `koanf:"disable_command_execute" json:"disable_command_execute,omitempty"`
	// WebSSH 终端
	DisableTerminal bool `koanf:"disable_terminal" json:"disable_terminal,omitempty"`
	// 文件管理器、MCP 文件读写与传输
	DisableFileManager bool `koanf:"disable_file_manager" json:"disable_file_manager,omitempty"`
	// 远程查看 / 修改 agent 配置
	DisableAgentConfig bool `koanf:"disable_agent_config" json:"disable_agent_config,omitempty"`
	// 服务器迁移（会轮换 agent 凭据并改写其配置）
	DisableServerTransfer bool `koanf:"disable_server_transfer" json:"disable_server_transfer,omitempty"`
	// 远程强制升级 agent
	DisableAgentUpgrade bool `koanf:"disable_agent_upgrade" json:"disable_agent_upgrade,omitempty"`
	// 内网穿透（经面板访问 agent 本机或内网服务）
	DisableNAT bool `koanf:"disable_nat" json:"disable_nat,omitempty"`
	// 在线安装第三方前端。前端 JS 运行在每个访问者（含管理员）的浏览器里，
	// 管理员账号失守时可被用来植入恶意页面窃取登录态。开启后只能使用内置前端，
	// 已安装的第三方前端会自动退回内置前端。
	DisableFrontendInstall bool `koanf:"disable_frontend_install" json:"disable_frontend_install,omitempty"`
}

type securityRule struct {
	key     string
	enabled func(SecurityConf) bool
	types   []uint64
}

var securityRules = []securityRule{
	{"disable_command_execute", func(c SecurityConf) bool { return c.DisableCommandExecute }, []uint64{TaskTypeCommand, TaskTypeExec}},
	{"disable_terminal", func(c SecurityConf) bool { return c.DisableTerminal }, []uint64{TaskTypeTerminal, TaskTypeTerminalGRPC}},
	{"disable_file_manager", func(c SecurityConf) bool { return c.DisableFileManager }, []uint64{TaskTypeFM, TaskTypeFsList, TaskTypeFsRead, TaskTypeFsWrite, TaskTypeFsDelete, TaskTypeFsTransfer}},
	{"disable_agent_config", func(c SecurityConf) bool { return c.DisableAgentConfig }, []uint64{TaskTypeReportConfig, TaskTypeApplyConfig}},
	{"disable_server_transfer", func(c SecurityConf) bool { return c.DisableServerTransfer }, []uint64{TaskTypeServerTransferApply}},
	{"disable_agent_upgrade", func(c SecurityConf) bool { return c.DisableAgentUpgrade }, []uint64{TaskTypeUpgrade}},
	{"disable_nat", func(c SecurityConf) bool { return c.DisableNAT }, []uint64{TaskTypeNAT}},
}

// TaskDisabledError 表示任务类型被面板安全策略禁止下发。
type TaskDisabledError struct {
	TaskType uint64
	Key      string
}

func (e *TaskDisabledError) Error() string {
	return fmt.Sprintf("task type %d is disabled by dashboard security policy (security.%s in config.yaml)", e.TaskType, e.Key)
}

var blockedTaskTypes atomic.Pointer[map[uint64]string]

// ApplySecurityPolicy 在启动读取配置后调用，之后所有 SendTask 都按此策略过滤。
func ApplySecurityPolicy(conf SecurityConf) {
	blocked := make(map[uint64]string)
	for _, r := range securityRules {
		if r.enabled(conf) {
			for _, t := range r.types {
				blocked[t] = r.key
			}
		}
	}
	blockedTaskTypes.Store(&blocked)
}

// CheckTaskAllowed 返回该任务类型是否允许下发给 agent。
func CheckTaskAllowed(taskType uint64) error {
	m := blockedTaskTypes.Load()
	if m == nil {
		return nil
	}
	if key, ok := (*m)[taskType]; ok {
		return &TaskDisabledError{TaskType: taskType, Key: key}
	}
	return nil
}

// SecurityPolicySummary 列出已关闭与仍开启的高危能力，供启动自检输出。
func (c SecurityConf) SecurityPolicySummary() (disabled, enabled []string) {
	for _, r := range securityRules {
		if r.enabled(c) {
			disabled = append(disabled, r.key)
		} else {
			enabled = append(enabled, strings.TrimPrefix(r.key, "disable_"))
		}
	}
	if c.DisableFrontendInstall {
		disabled = append(disabled, "disable_frontend_install")
	} else {
		enabled = append(enabled, "frontend_install")
	}
	sort.Strings(disabled)
	return disabled, enabled
}
