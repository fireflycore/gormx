package gormx

import (
	"errors"
	"strings"
	"time"

	"github.com/fireflycore/go-utils/network"
	"github.com/fireflycore/go-utils/tlsx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/opentelemetry-go-extra/otelgorm"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type PostgresConfig struct {
	Config
}

type PostgresDB struct {
	DB *gorm.DB
}

// NewPostgres 使用配置初始化 Postgres 的 gorm.DB，并可选执行 AutoMigrate。
func NewPostgres(config *PostgresConfig) (*PostgresDB, error) {
	if config == nil {
		return nil, errors.New("postgres: conf is nil")
	}

	// 将 address 拆为 host/port，未带端口时使用默认值 5432。
	host, port, err := network.SplitHostPort(config.Address, "5432")
	// 解析失败直接返回错误。
	if err != nil {
		return nil, err
	}

	// dsnParts 为 key=value 形式的 DSN 片段，最终将用空格拼接。
	dsnParts := []string{
		"host=" + host,
		"port=" + port,
		"dbname=" + config.Database,
		"TimeZone=UTC",
	}
	// 若提供用户名，则写入 DSN。
	if config.Username != "" {
		dsnParts = append(dsnParts, "user="+config.Username)
		// 若提供密码，则写入 DSN。
		if config.Password != "" {
			dsnParts = append(dsnParts, "password="+config.Password)
		}
	}

	// tlsConfig 为构造好的 TLS 配置；tlsEnabled 表示是否启用；err 为构造过程的错误。
	tlsConfig, tlsEnabled, err := tlsx.NewTLSConfig(config.Tls)
	// 构造 TLS 配置失败直接返回错误。
	if err != nil {
		return nil, err
	}
	// 启用 TLS 时设置 sslmode=require，否则显式禁用 ssl。
	if tlsEnabled {
		// require 表示强制使用 TLS。
		dsnParts = append(dsnParts, "sslmode=require")
	} else {
		// disable 表示不使用 TLS。
		dsnParts = append(dsnParts, "sslmode=disable")
	}

	// 将 DSN 解析为 pgx 连接配置。
	connConfig, err := pgx.ParseConfig(strings.Join(dsnParts, " "))
	// 解析失败直接返回错误。
	if err != nil {
		return nil, err
	}
	// 若启用 TLS，则把 tlsConfig 注入到 pgx 连接配置中。
	if tlsEnabled {
		connConfig.TLSConfig = tlsConfig
		connConfig.TLSConfig.ServerName = host
	}

	// 使用 pgx stdlib 将 ConnConfig 转成 *sql.DB，交给 gorm driver 复用连接池。
	sqlDB := stdlib.OpenDB(*connConfig)

	// log 根据配置构造（默认丢弃输出，开启 Logger 时输出）。
	log := NewLogger(&config.Config)

	// 打开 gorm DB，并配置命名策略、NowFunc、事务与 logger 等选项。
	db, err := gorm.Open(postgres.New(postgres.Config{
		// Conn 复用上面创建的 *sql.DB。
		Conn: sqlDB,
	}), &gorm.Config{
		// NamingStrategy 控制表名前缀与单复数规则。
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   config.TablePrefix,
			SingularTable: config.SingularTable,
		},
		// NowFunc 统一生成 UTC 时间。
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		// DisableForeignKeyConstraintWhenMigrating 控制迁移时是否创建外键。
		DisableForeignKeyConstraintWhenMigrating: config.DisableForeignKeyConstraintWhenMigrating,
		// SkipDefaultTransaction 控制 gorm 默认事务行为。
		SkipDefaultTransaction: config.SkipDefaultTransaction,
		// PrepareStmt 控制是否启用预处理语句。
		PrepareStmt: config.PrepareStmt,
		// Logger 为 gorm 的日志实现。
		Logger: log,
	})
	// 打开失败时关闭 sqlDB，避免连接泄漏。
	if err != nil {
		_ = sqlDB.Close()
		return nil, err
	}

	// 启用 otelgorm 插件（Tracing）
	if err = db.Use(otelgorm.NewPlugin(otelgorm.WithDBName(config.Database))); err != nil {
		return nil, err
	}

	// 当启用 autoMigrate 且传入表模型时，执行自动迁移。
	if len(config.tables) != 0 && config.autoMigrate {
		// AutoMigrate 会创建/修改表结构以匹配模型。
		if err = db.AutoMigrate(config.tables...); err != nil {
			return nil, err
		}
	}

	// 获取底层 *sql.DB 以配置连接池参数。
	d, err := db.DB()
	// 获取失败直接返回错误。
	if err != nil {
		return nil, err
	}

	// 设置最大打开连接数。
	d.SetMaxOpenConns(config.MaxOpenConnects)
	// 设置最大空闲连接数。
	d.SetMaxIdleConns(config.MaxIdleConnects)
	// ConnMaxLifeTime 约定为秒，<=0 表示不限制。
	if config.ConnMaxLifeTime > 0 {
		d.SetConnMaxLifetime(time.Second * time.Duration(config.ConnMaxLifeTime))
	} else {
		d.SetConnMaxLifetime(0)
	}

	// 返回封装后的 PostgresDB。
	return &PostgresDB{DB: db}, nil
}
