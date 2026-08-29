## gormx

基于 gorm 的工程化封装，提供：
- MySQL/Postgres 初始化（TLS、连接池、命名策略、可选 AutoMigrate）
- **全量可观测性集成**（OpenTelemetry Logs + Traces）
- 常用模型基类（自增主键、UUIDv7 主键、软删除）
- 常用 scope（分页）

## 安装

```bash
go get github.com/fireflycore/gormx
```

## 快速开始

### MySQL

```go
package main

import (
	"github.com/fireflycore/gormx"
)

func main() {
	conf := &gormx.MysqlConf{
		Conf: gormx.Conf{
			Type:            gormx.Mysql,
			Address:         "127.0.0.1:3306",
			Database:        "demo",
			Username:        "root",
			Password:        "root",
			MaxOpenConnects: 100,
			MaxIdleConnects: 10,
			ConnMaxLifeTime: 600,
			SingularTable:   true,
			PrepareStmt:     true,
			Logger:          true, // 开启后自动启用 OTel Logs
		},
	}

	conf.WithLoggerConsole(true)
	conf.WithAutoMigrate(true)

	db, err := gormx.NewMysql(conf, []interface{}{})
	if err != nil {
		panic(err)
	}

	_ = db.DB
}
```

### Postgres

```go
package main

import (
	"github.com/fireflycore/gormx"
)

func main() {
	conf := &gormx.PostgresConf{
		Conf: gormx.Conf{
			Type:            gormx.Postgres,
			Address:         "127.0.0.1:5432",
			Database:        "demo",
			Username:        "postgres",
			Password:        "postgres",
			MaxOpenConnects: 100,
			MaxIdleConnects: 10,
			ConnMaxLifeTime: 600,
			SingularTable:   true,
			PrepareStmt:     true,
			Logger:          true, // 开启后自动启用 OTel Logs
		},
	}

	conf.WithLoggerConsole(true)
	conf.WithAutoMigrate(true)

	db, err := gormx.NewPostgres(conf, []interface{}{})
	if err != nil {
		panic(err)
	}

	_ = db.DB
}
```

## 配置说明

初始化配置为 gormx.Conf，MySQL/Postgres 的配置结构分别为 gormx.MysqlConf / gormx.PostgresConf（匿名嵌入 Conf）。

常用字段：
- Address：MySQL 为 host:port；Postgres 可为 host 或 host:port（未带端口默认 5432）
- Database/Username/Password：连接信息
- MaxOpenConnects/MaxIdleConnects/ConnMaxLifeTime：连接池（ConnMaxLifeTime 单位为秒，<=0 表示不限制）
- TablePrefix/SingularTable：命名策略
- DisableForeignKeyConstraintWhenMigrating：AutoMigrate 时不创建物理外键
- SkipDefaultTransaction：跳过 gorm 默认事务
- PrepareStmt：启用预处理语句缓存
- Logger：启用 SQL 日志（自动上报 OpenTelemetry Logs，配合 WithLoggerConsole 可同时输出到控制台）

### TLS

当 Conf.Tls 同时配置了 CaCert / ClientCert / ClientCertKey 三个文件路径时启用 TLS，否则视为不启用：

```go
conf := &gormx.PostgresConf{
	Conf: gormx.Conf{
		Type:     gormx.Postgres,
		Address:  "127.0.0.1",
		Database: "demo",
		Username: "postgres",
		Password: "postgres",
		Tls: &gormx.TLS{
			CaCert:        "/path/to/ca.pem",
			ClientCert:    "/path/to/client.pem",
			ClientCertKey: "/path/to/client.key",
		},
	},
}
```

## 可观测性 (Observability)

gormx 已全量集成 OpenTelemetry，无需手动配置插件，只需确保你的应用已初始化全局 OTel Tracer/Logger Provider（例如使用 go-micro 框架）。

### 1. Logs (日志审计)

开启 `Conf.Logger = true` 后，gormx 会自动通过 OTel Logs SDK 上报每条 SQL 执行记录（OperationLog）。
- **Log Type**: `operation`
- **Fields**: `database`, `statement`, `result`, `duration`, `rows`, `trace_id`, `user_id`, `app_id`, `tenant_id` 等。
- **Destination**: 通常发往 OTel Collector -> Loki。

**注意**：
- 必须使用 `db.WithContext(ctx)` 执行 SQL，否则无法提取 TraceID 和 UserID。
- UserID/TenantID 等字段会自动从 gRPC metadata 中提取（如果存在）。

### 2. Traces (链路追踪)

初始化数据库时，gormx 会自动挂载 `otelgorm` 插件。
- 自动为每个 SQL 操作创建 Span。
- Span 名称格式：`SELECT demo.users`。
- **Destination**: 通常发往 OTel Collector -> Tempo/Jaeger。

**注意**：
- 如果未初始化全局 TracerProvider，插件会自动静默，不会报错。
- 同样需要 `db.WithContext(ctx)` 才能将 SQL Span 正确关联到父 Trace。

## 模型基类

gormx 提供了一组可直接嵌入的模型基类：
- gormx.Table：uint64 主键 + 软删除
- gormx.TableUnique：uint64 主键 + 软删除（DeletedAt 上 uniqueIndex:idx_unique）
- gormx.TableUUID：string 主键（UUIDv7）+ 软删除
- gormx.TableUUIDUnique：string 主键（UUIDv7）+ 软删除（DeletedAt 上 uniqueIndex:idx_unique）

### 分页 Scope

```go
import "github.com/fireflycore/gormx/scope"

db = db.Scopes(scope.WithPagination(1, 20))
```

分页规则：
- page 从 1 开始（0 会被修正为 1）
- size 范围为 [5, 100]（小于 5 修正为 5，大于 100 修正为 100）

## 行权限执行

`gormx/access` 将业务服务已经从 authz 获得并校验过的结构化行范围追加到 GORM 查询中。它不读取用户身份、不调用 authz、不加载 Casbin 策略，也不接受客户端传入的表名、列名或 SQL。

资源服务在代码中静态声明 `ResourceBinding`，再把决策转换为 `RowAccessDecision`：

```go
import (
	"time"

	"github.com/fireflycore/gormx/access"
	"gorm.io/gorm/clause"
)

binding := access.ResourceBinding{
	ResourceKey: "app.application",
	Table:       clause.Table{Name: "applications"},
	AppColumn:   clause.Column{Name: "app_id"},
	TenantColumn: clause.Column{Name: "tenant_id"},
	OwnerColumn: clause.Column{Name: "owner_id"},
}

query, err := access.Apply(
	db.Model(&Application{}),
	binding,
	access.RowAccessDecision{
		Allowed:     true,
		ResourceKey: "app.application",
		RowConstraints: []access.RowConstraint{{
			Dimension: access.ScopeDimensionTenant,
			Refs:      []string{"tenant-1"},
		}},
		ExpiresAt: time.Now().Add(time.Minute),
	},
)
if err != nil {
	return err
}

var applications []Application
if err := query.Find(&applications).Error; err != nil {
	return err
}
```

应用、租户、用户、owner、资源和组织范围会以参数化 `IN` 条件追加，并按不同维度组合为 `AND`。组织下级范围和业务关系范围由资源服务实现静态 resolver；resolver 应使用独立的 GORM 子查询会话，避免把外层查询条件带入子查询。

`Apply` 在决策拒绝、过期、资源不匹配、重复/非法范围、绑定列缺失或 resolver 失败时返回错误。调用方必须停止使用原始查询，不能忽略错误后绕过行权限。`gormx/access` 不负责字段白名单、DTO 脱敏、创建归属校验或更新字段映射，这些规则仍由业务服务的 biz/data 层执行。

该包的模拟数据测试使用 SQLite，仅用于验证参数化范围和 fail-close 行为；生产服务仍使用项目配置的 PostgreSQL 或 MySQL 连接。

### 基准测试

执行器基准包含 DryRun 构建开销、20,000 行 SQLite 模拟表上的受限列表查询，以及无范围查询对照：

```bash
go test -run '^$' -bench '^Benchmark' -benchmem ./access
```

该基准用于比较 `Apply` 与手写同等 `WHERE` 的相对开销，不代表 PostgreSQL 生产吞吐。生产性能主要取决于网络往返、数据量、索引和 resolver 子查询计划；应在目标 PostgreSQL 版本、真实数据分布和实际索引上另行压测。
