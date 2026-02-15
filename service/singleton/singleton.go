package singleton

import (
	_ "embed"
	"fmt"
	"iter"
	"log"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/patrickmn/go-cache"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
	"sigs.k8s.io/yaml"

	"github.com/nezhahq/nezha/model"
	"github.com/nezhahq/nezha/pkg/utils"
)

var Version = "debug"

var (
	Cache             *cache.Cache
	DB                *gorm.DB
	Loc               *time.Location
	FrontendTemplates []model.FrontendTemplate
	DashboardBootTime = uint64(time.Now().Unix())

	ServerShared          *ServerClass
	ServiceSentinelShared *ServiceSentinel
	DDNSShared            *DDNSClass
	NotificationShared    *NotificationClass
	NATShared             *NATClass
	CronShared            *CronClass
)

//go:embed frontend-templates.yaml
var frontendTemplatesYAML []byte

func InitTimezoneAndCache() error {
	var err error
	Loc, err = time.LoadLocation(Conf.Location)
	if err != nil {
		return err
	}

	Cache = cache.New(5*time.Minute, 10*time.Minute)
	return nil
}

// LoadSingleton 加载子服务并执行
func LoadSingleton(bus chan<- *model.Service) (err error) {
	initI18n() // 加载本地化服务
	initUser() // 加载用户ID绑定表
	NATShared = NewNATClass()
	DDNSShared = NewDDNSClass()
	NotificationShared = NewNotificationClass()
	ServerShared = NewServerClass()
	CronShared = NewCronClass()
	// 最后初始化 ServiceSentinel
	ServiceSentinelShared, err = NewServiceSentinel(bus)
	return
}

// InitFrontendTemplates 从内置文件中加载FrontendTemplates
func InitFrontendTemplates() error {
	err := yaml.Unmarshal(frontendTemplatesYAML, &FrontendTemplates)
	if err != nil {
		return err
	}
	return nil
}

// InitDBFromPath 从给出的文件路径中加载数据库 (向后兼容)
// Deprecated: Use InitDB with DatabaseConfig instead
func InitDBFromPath(path string) error {
	return InitDB(model.DatabaseConfig{
		Type: model.DBTypeSQLite,
		Path: path,
	})
}

// InitDB 根据配置初始化数据库连接
// Initialize database connection based on configuration
func InitDB(dbConfig model.DatabaseConfig) error {
	var err error
	var dialector gorm.Dialector

	switch dbConfig.Type {
	case model.DBTypeMySQL:
		dialector = mysql.Open(dbConfig.DSN())
		log.Printf("NEZHA>> Using MySQL database at %s:%d", dbConfig.Host, dbConfig.Port)
	case model.DBTypePostgres:
		dialector = postgres.Open(dbConfig.DSN())
		log.Printf("NEZHA>> Using PostgreSQL database at %s:%d", dbConfig.Host, dbConfig.Port)
	case model.DBTypeSQLServer:
		dialector = sqlserver.Open(dbConfig.DSN())
		log.Printf("NEZHA>> Using SQL Server database at %s:%d", dbConfig.Host, dbConfig.Port)
	case model.DBTypeSQLite, "":
		// Default to SQLite for backward compatibility
		path := dbConfig.Path
		if path == "" {
			path = "data/sqlite.db"
		}
		dialector = sqlite.Open(path)
		log.Printf("NEZHA>> Using SQLite database at %s", path)
	default:
		return fmt.Errorf("unsupported database type: %s", dbConfig.Type)
	}

	DB, err = gorm.Open(dialector, &gorm.Config{
		CreateBatchSize: 200,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	if Conf.Debug {
		DB = DB.Debug()
	}
	err = DB.AutoMigrate(model.Server{}, model.User{}, model.ServerGroup{}, model.NotificationGroup{},
		model.Notification{}, model.AlertRule{}, model.Service{}, model.NotificationGroupNotification{},
		model.ServiceHistory{}, model.Cron{}, model.Transfer{}, model.ServerGroupServer{},
		model.NAT{}, model.DDNSProfile{}, model.NotificationGroupNotification{},
		model.WAF{}, model.Oauth2Bind{})
	if err != nil {
		return fmt.Errorf("failed to auto-migrate database: %w", err)
	}

	// 执行数据库迁移（处理容器升级时的类型转换等）
	model.RunMigrations(DB, dbConfig.Type)

	return nil
}

// RecordTransferHourlyUsage 对流量记录进行打点
func RecordTransferHourlyUsage(servers ...*model.Server) {
	now := time.Now()
	nowTrimSeconds := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())

	var txs []model.Transfer
	var slist iter.Seq[*model.Server]
	if len(servers) > 0 {
		slist = slices.Values(servers)
	} else {
		slist = utils.Seq2To1(ServerShared.Range)
	}

	for server := range slist {
		tx := model.Transfer{
			ServerID: server.ID,
			In:       utils.SubUintChecked(server.State.NetInTransfer, server.PrevTransferInSnapshot),
			Out:      utils.SubUintChecked(server.State.NetOutTransfer, server.PrevTransferOutSnapshot),
		}
		if tx.In == 0 && tx.Out == 0 {
			continue
		}
		server.PrevTransferInSnapshot = server.State.NetInTransfer
		server.PrevTransferOutSnapshot = server.State.NetOutTransfer
		tx.CreatedAt = nowTrimSeconds
		txs = append(txs, tx)
	}

	if len(txs) == 0 {
		return
	}
	log.Printf("NEZHA>> Saved traffic metrics to database. Affected %d row(s), Error: %v", len(txs), DB.Create(txs).Error)
}

// CleanMonitorHistory 清理无效或过时的 监控记录 和 流量记录
// 兼容 MySQL/PostgreSQL/SQLite/SQL Server
func CleanMonitorHistory() {
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)
	oneDayAgo := time.Now().AddDate(0, 0, -1)

	if !TSDBEnabled() {
		// 仅在未启用 TSDB 时清理 ServiceHistory 表
		// 清理已被删除的服务的监控记录 + 超过30天的记录
		DB.Unscoped().Where("created_at < ? OR service_id NOT IN (SELECT id FROM services)", thirtyDaysAgo).Delete(&model.ServiceHistory{})
		// 清理超过1天的详细监控记录（server_id != 0 的逐服务器记录）
		DB.Unscoped().Where("(created_at < ? AND server_id != 0) OR service_id NOT IN (SELECT id FROM services)", oneDayAgo).Delete(&model.ServiceHistory{})
	}

	// 清理已被删除的服务器的流量记录
	DB.Unscoped().Where("server_id NOT IN (SELECT id FROM servers)").Delete(&model.Transfer{})

	// 计算可清理流量记录的时长
	var allServerKeep time.Time
	specialServerKeep := make(map[uint64]time.Time)
	var specialServerIDs []uint64
	var alerts []model.AlertRule
	DB.Find(&alerts)
	for _, alert := range alerts {
		for _, rule := range alert.Rules {
			// 是不是流量记录规则
			if !rule.IsTransferDurationRule() {
				continue
			}
			dataCouldRemoveBefore := rule.GetTransferDurationStart().UTC()
			// 判断规则影响的机器范围
			if rule.Cover == model.RuleCoverAll {
				// 更新全局可以清理的数据点
				if allServerKeep.IsZero() || allServerKeep.After(dataCouldRemoveBefore) {
					allServerKeep = dataCouldRemoveBefore
				}
			} else {
				// 更新特定机器可以清理数据点
				for id := range rule.Ignore {
					if specialServerKeep[id].IsZero() || specialServerKeep[id].After(dataCouldRemoveBefore) {
						specialServerKeep[id] = dataCouldRemoveBefore
						specialServerIDs = append(specialServerIDs, id)
					}
				}
			}
		}
	}
	for id, couldRemove := range specialServerKeep {
		DB.Unscoped().Where("server_id = ? AND created_at < ?", id, couldRemove).Delete(&model.Transfer{})
	}
	if allServerKeep.IsZero() {
		if len(specialServerIDs) > 0 {
			DB.Unscoped().Where("server_id NOT IN (?)", specialServerIDs).Delete(&model.Transfer{})
		}
	} else {
		if len(specialServerIDs) > 0 {
			DB.Unscoped().Where("server_id NOT IN (?) AND created_at < ?", specialServerIDs, allServerKeep).Delete(&model.Transfer{})
		} else {
			DB.Unscoped().Where("created_at < ?", allServerKeep).Delete(&model.Transfer{})
		}
	}
}

// IPDesensitize 根据设置选择是否对IP进行打码处理 返回处理后的IP(关闭打码则返回原IP)
func IPDesensitize(ip string) string {
	if Conf.EnablePlainIPInNotification {
		return ip
	}
	return utils.IPDesensitize(ip)
}

type class[K comparable, V model.CommonInterface] struct {
	list   map[K]V
	listMu sync.RWMutex

	sortedList   []V
	sortedListMu sync.RWMutex
}

func (c *class[K, V]) Get(id K) (s V, ok bool) {
	c.listMu.RLock()
	defer c.listMu.RUnlock()

	s, ok = c.list[id]
	return
}

func (c *class[K, V]) GetList() map[K]V {
	c.listMu.RLock()
	defer c.listMu.RUnlock()

	return maps.Clone(c.list)
}

func (c *class[K, V]) GetSortedList() []V {
	c.sortedListMu.RLock()
	defer c.sortedListMu.RUnlock()

	return slices.Clone(c.sortedList)
}

func (c *class[K, V]) Range(fn func(k K, v V) bool) {
	c.listMu.RLock()
	defer c.listMu.RUnlock()

	for k, v := range c.list {
		if !fn(k, v) {
			break
		}
	}
}

func (c *class[K, V]) CheckPermission(ctx *gin.Context, idList iter.Seq[K]) bool {
	c.listMu.RLock()
	defer c.listMu.RUnlock()

	for id := range idList {
		if s, ok := c.list[id]; ok {
			if !s.HasPermission(ctx) {
				return false
			}
		}
	}
	return true
}
