# gormx/access

`gormx/access` 将业务服务从 `authz.AuthorizeDataAccess` 获得并完成本地校验的结构化行权限决策，翻译为参数化的 GORM 查询条件。

本包只负责执行行范围，不负责计算权限：

- 不读取用户身份，不调用 `authz`，不加载 Casbin 策略。
- 不依赖 `go-micro`、permission Proto 或业务实体。
- 不接受客户端传入的表名、列名、排序表达式、join 或 SQL 片段。
- 不处理字段白名单、DTO 脱敏、创建归属校验和更新字段映射；这些规则由业务服务的 biz/data 层负责。

## 基本用法

资源所属服务应在代码中静态声明 `ResourceBinding`，并将上游决策转换为 `RowAccessDecision`：

```go
import (
	"time"

	"github.com/fireflycore/gormx/access"
	"gorm.io/gorm/clause"
)

var applicationBinding = access.ResourceBinding{
	ResourceKey:    "app.application",
	Table:          clause.Table{Name: "applications"},
	AppColumn:      clause.Column{Name: "app_id"},
	TenantColumn:   clause.Column{Name: "tenant_id"},
	UserColumn:     clause.Column{Name: "user_id"},
	OwnerColumn:    clause.Column{Name: "owner_id"},
	ResourceColumn: clause.Column{Name: "id"},
}

decision := access.RowAccessDecision{
	Allowed:     true,
	ResourceKey: "app.application",
	RowConstraints: []access.RowConstraint{{
		Dimension: access.ScopeDimensionTenant,
		Refs:      []string{"tenant-1"},
	}},
	ExpiresAt: time.Now().Add(time.Minute),
}

scoped, err := access.Apply(db.Model(&Application{}), applicationBinding, decision)
if err != nil {
	return err
}

var applications []Application
if err := scoped.Order("created_at DESC").Limit(20).Find(&applications).Error; err != nil {
	return err
}
```

`Apply` 返回一个新的 GORM 查询对象。调用方必须使用这个返回值，不能在出错后继续使用未加范围的原始 `db`。

## 范围维度

| 常量 | 含义 | 绑定字段/处理方式 |
| --- | --- | --- |
| `ScopeDimensionApplication` | 应用范围 | `AppColumn` |
| `ScopeDimensionTenant` | 租户范围 | `TenantColumn` |
| `ScopeDimensionOrganization` | 组织范围 | `Organization.Column`；下级组织使用 `DescendantResolver` |
| `ScopeDimensionUser` | 用户主体范围 | `UserColumn` |
| `ScopeDimensionOwner` | 资源 owner 范围 | `OwnerColumn` |
| `ScopeDimensionRelation` | 业务关系范围 | `RelationResolver` 按静态 `RelationKey` 解释 |
| `ScopeDimensionResource` | 指定资源实例范围 | `ResourceColumn` |
| `ScopeDimensionAll` | 显式不限制行范围 | 不追加条件 |

同一决策中的不同硬维度会追加为 `AND`，每个维度的引用值使用参数化 `IN` 条件。上游 `authz` 应先完成同一维度范围的合并；`gormx/access` 不自行合并策略，也不接受同一维度重复约束。

## 组织和关系 resolver

组织下级和业务关系不能把逻辑键直接当作列名或 SQL。资源服务通过静态 resolver 把它们翻译为本地 GORM 条件：

```go
import (
	"github.com/fireflycore/gormx/access"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

binding.Organization.DescendantResolver = access.OrganizationDescendantResolverFunc(
	func(db *gorm.DB, table clause.Table, column clause.Column, refs []string) (*gorm.DB, error) {
		// 使用固定的组织表、列和参数化引用构造子查询。
		subquery := db.Session(&gorm.Session{NewDB: true}).
			Table("organizations").Select("id").Where("path LIKE ?", refs[0]+"%")
		return db.Where("? IN (?)", column, subquery), nil
	},
)

binding.RelationResolver = access.RelationResolverFunc(
	func(db *gorm.DB, table clause.Table, relationKey string, refs []string) (*gorm.DB, error) {
		if relationKey != "application.member" {
			return nil, access.ErrRelationResolverMissing
		}
		return db.Where("owner_id IN ?", refs), nil
	},
)
```

resolver 必须只使用服务端固定的表、列和关系键，并对 `refs` 使用 GORM 参数绑定。构造独立子查询时应使用 `db.Session(&gorm.Session{NewDB: true})`，避免把外层查询条件带入子查询。

## 错误语义

`Apply` 在以下情况返回错误并 fail-close：

- `ErrBindingInvalid`：资源键、表名或静态绑定不合法。
- `ErrDecisionInvalid`：资源键不匹配、决策过期、范围为空、重复或包含非法选项。
- `ErrAccessDenied`：上游决策明确拒绝。
- `ErrUnsupportedDimension`：所需的本地列未声明或列被标记为 raw。
- `ErrRelationResolverMissing`：relation 范围没有 resolver。
- `ErrOrganizationResolverMissing`：组织下级范围没有 resolver。

资源服务通常将这些错误映射为内部的 `PermissionDenied` 或服务错误；受限 Info 查询无命中时不要发起无范围查询来区分“资源不存在”和“资源不可见”。List/数组场景按服务约定返回空集合。

## 与数据库和事务的关系

`Apply` 只构造查询，不创建事务、不执行迁移，也不修改数据库配置。调用方应复用现有 GORM 事务和连接池，并在 `Count`、`Find`、更新或删除前使用同一受限查询范围。

生产性能主要取决于 PostgreSQL/MySQL 的索引、数据分布、resolver 子查询计划和网络往返；应在目标数据库上压测，不能用内存 SQLite 的数字代表生产吞吐。
