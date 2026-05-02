package gormx

import "github.com/fireflycore/go-utils/tlsx"

// Config 为 gorm 初始化所需的配置项集合。
type Config struct {
	// 1-postgres 2-oracle 3-sqlite 4-mysql 5-mssql
	// Type 表示数据库类型枚举。
	Type uint32 `json:"type"`
	// TLS加密配置（生产环境建议启用），如果不为null则启用tls加密
	// Tls 为 TLS 配置，非空时启用 TLS。
	Tls *tlsx.TLS `json:"tls"`

	// Address 为数据库地址，一般为 host:port。
	Address string `json:"address"`
	// Database 为数据库名称。
	Database string `json:"database"`
	// Username 为数据库用户名。
	Username string `json:"username"`
	// Password 为数据库密码。
	Password string `json:"password"`

	// 表名前缀
	// TablePrefix 会拼接到 gorm 的表名之前。
	TablePrefix string `json:"table_prefix"`

	// 最大打开连接数（建议：根据负载设置，默认100），0表示无限制（不推荐生产环境使用）
	// MaxOpenConnects 会设置到 database/sql 连接池的 MaxOpenConns。
	MaxOpenConnects int `json:"max_open_connects"`
	// 最大空闲连接数（建议：保持适当空闲连接减少握手开销），0表示无限制（需配合max_open_connects使用）
	// MaxIdleConnects 会设置到 database/sql 连接池的 MaxIdleConns。
	MaxIdleConnects int `json:"max_idle_connects"`
	// 连接最大生命周期（单位：秒，建议：300-600秒），超时后连接会被强制回收重建
	// ConnMaxLifeTime 会设置到 database/sql 连接池的 ConnMaxLifetime（按秒）。
	ConnMaxLifeTime int `json:"conn_max_life_time"`

	// 是否为单数表名
	// SingularTable 为 true 时，表名不做复数化。
	SingularTable bool `json:"singular_table"`
	// 是否禁用物理外键
	// DisableForeignKeyConstraintWhenMigrating 为 true 时，自动迁移时不创建物理外键。
	DisableForeignKeyConstraintWhenMigrating bool `json:"disable_foreign_key_constraint_when_migrating"`
	// 是否跳过默认事务（特殊场景使用，如批量导入）
	// SkipDefaultTransaction 为 true 时，gorm 默认写操作不启事务。
	SkipDefaultTransaction bool `json:"skip_default_transaction"`
	// 是否启用预处理语句（安全建议：始终开启防止SQL注入）
	// PrepareStmt 为 true 时，gorm 将启用预处理语句缓存。
	PrepareStmt bool `json:"prepare_stmt"`

	// 是否启用SQL日志（调试建议开启，生产环境建议关闭）
	// Logger 为 true 时启用 gorm logger，并可通过 WithLoggerConsole 控制输出。
	Logger bool `json:"logger"`

	// autoMigrate 控制 NewMysql/NewPostgres 是否执行 AutoMigrate。
	autoMigrate bool
	// loggerConsole 控制是否输出到控制台。
	loggerConsole bool
}

// WithLoggerConsole 设置是否将 SQL 日志输出到控制台。
func (c *Config) WithLoggerConsole(state bool) {
	c.loggerConsole = state
}

// WithAutoMigrate 设置是否在初始化连接后自动迁移表结构。
func (c *Config) WithAutoMigrate(state bool) {
	c.autoMigrate = state
}
