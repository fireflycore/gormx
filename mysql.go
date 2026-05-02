package gormx

import (
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/fireflycore/go-utils/network"
	"github.com/fireflycore/go-utils/tlsx"
	"github.com/go-sql-driver/mysql"
	"github.com/uptrace/opentelemetry-go-extra/otelgorm"
	mysql2 "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type MysqlConfig struct {
	Config
}

type MysqlDB struct {
	DB *gorm.DB
}

// mysqlTLSConfigSeq 用于生成唯一的 TLS 配置名，避免重复注册冲突。
var mysqlTLSConfigSeq uint64

// NewMysql 使用配置初始化 MySQL 的 gorm.DB，并可选执行 AutoMigrate。
func NewMysql(mc *MysqlConfig, tables []interface{}) (*MysqlDB, error) {
	if mc == nil {
		return nil, errors.New("mysql: conf is nil")
	}

	host, port, err := network.SplitHostPort(mc.Address, "3306")
	if err != nil {
		return nil, err
	}

	// clientOptions 为 go-sql-driver/mysql 的连接配置。
	clientOptions := mysql.Config{
		Net:    "tcp",
		Addr:   net.JoinHostPort(host, port),
		DBName: mc.Database,
		// Loc 统一使用 UTC。
		Loc: time.UTC,
		// ParseTime 让 time 类型字段可被正确扫描。
		ParseTime: true,
	}

	if mc.Username != "" {
		clientOptions.User = mc.Username
		if mc.Password != "" {
			clientOptions.Passwd = mc.Password
		}
	}

	// tlsConfig 为构造好的 TLS 配置；tlsEnabled 表示是否启用；err 为构造过程的错误。
	tlsConfig, tlsEnabled, err := tlsx.NewTLSConfig(mc.Tls)
	// 构造 TLS 配置失败直接返回错误。
	if err != nil {
		return nil, err
	}
	// 启用 TLS 时，需要把 TLS 配置注册到 go-sql-driver/mysql。
	if tlsEnabled {
		tlsConfig.ServerName = host
		// tlsConfigName 为全局唯一名，用于在 DSN 中引用对应的 TLS 配置。
		tlsConfigName := "gormx_" + strconv.FormatUint(atomic.AddUint64(&mysqlTLSConfigSeq, 1), 10)
		// RegisterTLSConfig 将 tlsConfigName -> tlsConfig 注册到全局表。
		if err = mysql.RegisterTLSConfig(tlsConfigName, tlsConfig); err != nil {
			return nil, err
		}
		// 将 DSN 中的 TLSConfig 指向上面注册的配置名。
		clientOptions.TLSConfig = tlsConfigName
	}

	// gormLogger 根据配置构造（默认丢弃输出，开启 Logger 时输出）。
	log := NewLogger(&mc.Config)

	db, err := gorm.Open(mysql2.Open(clientOptions.FormatDSN()), &gorm.Config{
		// NamingStrategy 控制表名前缀与单复数规则。
		NamingStrategy: schema.NamingStrategy{
			TablePrefix:   mc.TablePrefix,
			SingularTable: mc.SingularTable,
		},
		// NowFunc 统一生成 UTC 时间。
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
		// DisableForeignKeyConstraintWhenMigrating 控制迁移时是否创建外键。
		DisableForeignKeyConstraintWhenMigrating: mc.DisableForeignKeyConstraintWhenMigrating,
		// SkipDefaultTransaction 控制 gorm 默认事务行为。
		SkipDefaultTransaction: mc.SkipDefaultTransaction,
		// PrepareStmt 控制是否启用预处理语句。
		PrepareStmt: mc.PrepareStmt,
		// Logger 为 gorm 的日志实现。
		Logger: log,
	})
	// 打开失败直接返回错误。
	if err != nil {
		return nil, err
	}

	// 启用 otelgorm 插件（Tracing），如果配置了 Logger 开启。
	// 这里默认始终开启 Tracing，只要初始化了 DB；是否上报取决于外部是否有 TracerProvider。
	// 但为了稳妥，通常只有明确需要 observability 时才开启。
	// 鉴于用户要求“全量替换”，且 go-micro 侧有开关控制，这里我们总是尝试挂载插件。
	// 插件内部会检查全局 TracerProvider，如果没有注册则只会产生空操作，开销极小。
	if err = db.Use(otelgorm.NewPlugin(otelgorm.WithDBName(mc.Database))); err != nil {
		return nil, err
	}

	// 当启用 autoMigrate 且传入表模型时，执行自动迁移。
	if len(tables) != 0 && mc.autoMigrate {
		// AutoMigrate 会创建/修改表结构以匹配模型。
		if err = db.AutoMigrate(tables...); err != nil {
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
	d.SetMaxOpenConns(mc.MaxOpenConnects)
	// 设置最大空闲连接数。
	d.SetMaxIdleConns(mc.MaxIdleConnects)
	// ConnMaxLifeTime 约定为秒，<=0 表示不限制。
	if mc.ConnMaxLifeTime > 0 {
		d.SetConnMaxLifetime(time.Second * time.Duration(mc.ConnMaxLifeTime))
	} else {
		d.SetConnMaxLifetime(0)
	}

	// 返回封装后的 MysqlDB。
	return &MysqlDB{DB: db}, nil
}
