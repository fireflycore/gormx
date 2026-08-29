// Package access 将已经校验过的数据访问决策翻译为参数化 GORM 查询范围。
//
// 本包只依赖 GORM，不读取身份、不调用 authz，也不解释 permission 的领域类型。
// 资源服务必须通过静态 ResourceBinding 提供列映射；客户端不能通过决策注入表名、
// 列名、排序表达式或 SQL 片段。
package access

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// ErrBindingInvalid 表示静态资源绑定不完整或包含不安全映射。
	ErrBindingInvalid = errors.New("gormx/access: resource binding is invalid")
	// ErrDecisionInvalid 表示决策未绑定资源、已过期或包含非法范围。
	ErrDecisionInvalid = errors.New("gormx/access: row access decision is invalid")
	// ErrAccessDenied 表示决策明确拒绝当前资源访问。
	ErrAccessDenied = errors.New("gormx/access: row access is denied")
	// ErrUnsupportedDimension 表示资源未声明某个范围维度的本地映射。
	ErrUnsupportedDimension = errors.New("gormx/access: row access dimension is unsupported")
	// ErrRelationResolverMissing 表示 relation 范围没有可用的静态 resolver。
	ErrRelationResolverMissing = errors.New("gormx/access: relation resolver is missing")
	// ErrOrganizationResolverMissing 表示组织下级范围没有可用的静态 resolver。
	ErrOrganizationResolverMissing = errors.New("gormx/access: organization descendant resolver is missing")
)

// 范围维度编码与 permission/go-micro access 保持一致，但本包不依赖它们的类型定义。
const (
	ScopeDimensionApplication  uint32 = 1
	ScopeDimensionTenant       uint32 = 2
	ScopeDimensionOrganization uint32 = 3
	ScopeDimensionUser         uint32 = 4
	ScopeDimensionOwner        uint32 = 5
	ScopeDimensionRelation     uint32 = 6
	ScopeDimensionResource     uint32 = 7
	ScopeDimensionAll          uint32 = 8
)

// RowConstraint 是不含 SQL 的声明式行范围。
type RowConstraint struct {
	// Dimension 是范围维度编码。
	Dimension uint32
	// Refs 是该维度的参数化引用值集合。
	Refs []string
	// IncludeDescendants 表示组织范围是否包含下级节点。
	IncludeDescendants bool
	// RelationKey 是由资源服务静态登记的关系逻辑键。
	RelationKey string
}

// RowAccessDecision 是 gormx 执行所需的最小结构化决策。
// 它通常由业务服务从 go-micro/access 决策转换得到。
type RowAccessDecision struct {
	// Allowed 表示资源动作是否允许继续执行。
	Allowed bool
	// ResourceKey 绑定决策适用的逻辑资源。
	ResourceKey string
	// RowConstraints 是已经由 authz 合并后的行范围。
	RowConstraints []RowConstraint
	// ExpiresAt 是决策的最晚执行时间。
	ExpiresAt time.Time
}

// OrganizationBinding 描述组织列及下级组织范围 resolver。
type OrganizationBinding struct {
	// Column 是资源表中的组织归属列。
	Column clause.Column
	// DescendantResolver 将组织根节点引用解析为参数化的本地查询条件。
	DescendantResolver OrganizationDescendantResolver
}

// OrganizationDescendantResolver 由资源服务实现组织树查询。
// resolver 只能使用静态表和列映射构造 GORM 条件，不能接受客户端 SQL 片段。
type OrganizationDescendantResolver interface {
	Apply(*gorm.DB, clause.Table, clause.Column, []string) (*gorm.DB, error)
}

// OrganizationDescendantResolverFunc 将函数适配为组织下级 resolver。
type OrganizationDescendantResolverFunc func(*gorm.DB, clause.Table, clause.Column, []string) (*gorm.DB, error)

// Apply 实现 OrganizationDescendantResolver。
func (f OrganizationDescendantResolverFunc) Apply(db *gorm.DB, table clause.Table, column clause.Column, refs []string) (*gorm.DB, error) {
	if f == nil {
		return nil, ErrOrganizationResolverMissing
	}
	return f(db, table, column, refs)
}

// RelationResolver 由资源服务实现业务关系范围查询。
// relationKey 只能匹配服务端静态登记的逻辑键。
type RelationResolver interface {
	Apply(*gorm.DB, clause.Table, string, []string) (*gorm.DB, error)
}

// RelationResolverFunc 将函数适配为关系 resolver。
type RelationResolverFunc func(*gorm.DB, clause.Table, string, []string) (*gorm.DB, error)

// Apply 实现 RelationResolver。
func (f RelationResolverFunc) Apply(db *gorm.DB, table clause.Table, relationKey string, refs []string) (*gorm.DB, error) {
	if f == nil {
		return nil, ErrRelationResolverMissing
	}
	return f(db, table, relationKey, refs)
}

// ResourceBinding 是资源服务静态声明的逻辑资源到本地表列的绑定。
type ResourceBinding struct {
	// ResourceKey 是跨服务稳定的逻辑资源键。
	ResourceKey string
	// Table 是资源查询使用的静态表或别名。
	Table clause.Table
	// AppColumn 是业务应用归属列。
	AppColumn clause.Column
	// TenantColumn 是业务租户归属列。
	TenantColumn clause.Column
	// UserColumn 是用户主体归属列。
	UserColumn clause.Column
	// OwnerColumn 是资源 owner 归属列。
	OwnerColumn clause.Column
	// ResourceColumn 是资源 ID 列。
	ResourceColumn clause.Column
	// Organization 是组织归属列和下级解析器。
	Organization OrganizationBinding
	// RelationResolver 是业务关系范围解析器。
	RelationResolver RelationResolver
}

// Validate 校验静态绑定的最小安全结构。
func (b ResourceBinding) Validate() error {
	if strings.TrimSpace(b.ResourceKey) == "" {
		return fmt.Errorf("%w: resource key is empty", ErrBindingInvalid)
	}
	if strings.TrimSpace(b.Table.Name) == "" {
		return fmt.Errorf("%w: table is empty", ErrBindingInvalid)
	}
	if b.Table.Raw {
		return fmt.Errorf("%w: table must not be raw", ErrBindingInvalid)
	}
	for _, item := range []struct {
		name   string
		column clause.Column
	}{
		{name: "application", column: b.AppColumn},
		{name: "tenant", column: b.TenantColumn},
		{name: "user", column: b.UserColumn},
		{name: "owner", column: b.OwnerColumn},
		{name: "resource", column: b.ResourceColumn},
		{name: "organization", column: b.Organization.Column},
	} {
		if item.column.Raw {
			return fmt.Errorf("%w: %s column must not be raw", ErrBindingInvalid, item.name)
		}
	}
	return nil
}

// Validate 校验决策是否允许执行且尚未过期。
func (d RowAccessDecision) Validate(binding ResourceBinding, now time.Time) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(d.ResourceKey) == "" || d.ResourceKey != binding.ResourceKey {
		return fmt.Errorf("%w: resource key does not match binding", ErrDecisionInvalid)
	}
	if !d.Allowed {
		return ErrAccessDenied
	}
	if d.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: expires_at is missing", ErrDecisionInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !d.ExpiresAt.After(now) {
		return fmt.Errorf("%w: expires_at is not in the future", ErrDecisionInvalid)
	}
	return validateConstraints(d.RowConstraints)
}

// Apply 将结构化行范围追加到 GORM 查询，并在任何不安全输入下返回错误。
// 调用方必须使用返回的 *gorm.DB，不能忽略错误后继续执行原始查询。
func Apply(db *gorm.DB, binding ResourceBinding, decision RowAccessDecision) (*gorm.DB, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: db is nil", ErrBindingInvalid)
	}
	if err := decision.Validate(binding, time.Now().UTC()); err != nil {
		return nil, err
	}

	query := db
	seen := make(map[uint32]struct{}, len(decision.RowConstraints))
	for _, constraint := range decision.RowConstraints {
		if _, duplicate := seen[constraint.Dimension]; duplicate {
			return nil, fmt.Errorf("%w: duplicate dimension %d", ErrDecisionInvalid, constraint.Dimension)
		}
		seen[constraint.Dimension] = struct{}{}

		switch constraint.Dimension {
		case ScopeDimensionApplication:
			if err := requireColumn(binding.AppColumn, "application"); err != nil {
				return nil, err
			}
			query = query.Where(clause.IN{Column: bindColumn(binding.AppColumn), Values: stringValues(constraint.Refs)})
		case ScopeDimensionTenant:
			if err := requireColumn(binding.TenantColumn, "tenant"); err != nil {
				return nil, err
			}
			query = query.Where(clause.IN{Column: bindColumn(binding.TenantColumn), Values: stringValues(constraint.Refs)})
		case ScopeDimensionUser:
			if err := requireColumn(binding.UserColumn, "user"); err != nil {
				return nil, err
			}
			query = query.Where(clause.IN{Column: bindColumn(binding.UserColumn), Values: stringValues(constraint.Refs)})
		case ScopeDimensionOwner:
			if err := requireColumn(binding.OwnerColumn, "owner"); err != nil {
				return nil, err
			}
			query = query.Where(clause.IN{Column: bindColumn(binding.OwnerColumn), Values: stringValues(constraint.Refs)})
		case ScopeDimensionResource:
			if err := requireColumn(binding.ResourceColumn, "resource"); err != nil {
				return nil, err
			}
			query = query.Where(clause.IN{Column: bindColumn(binding.ResourceColumn), Values: stringValues(constraint.Refs)})
		case ScopeDimensionOrganization:
			if err := requireColumn(binding.Organization.Column, "organization"); err != nil {
				return nil, err
			}
			if constraint.IncludeDescendants {
				if binding.Organization.DescendantResolver == nil {
					return nil, ErrOrganizationResolverMissing
				}
				var err error
				query, err = binding.Organization.DescendantResolver.Apply(query, binding.Table, bindColumn(binding.Organization.Column), cloneRefs(constraint.Refs))
				if err != nil {
					return nil, err
				}
				if query == nil {
					return nil, fmt.Errorf("%w: organization resolver returned nil query", ErrDecisionInvalid)
				}
				if query.Error != nil {
					return nil, query.Error
				}
				continue
			}
			query = query.Where(clause.IN{Column: bindColumn(binding.Organization.Column), Values: stringValues(constraint.Refs)})
		case ScopeDimensionRelation:
			if binding.RelationResolver == nil {
				return nil, ErrRelationResolverMissing
			}
			var err error
			query, err = binding.RelationResolver.Apply(query, binding.Table, constraint.RelationKey, cloneRefs(constraint.Refs))
			if err != nil {
				return nil, err
			}
			if query == nil {
				return nil, fmt.Errorf("%w: relation resolver returned nil query", ErrDecisionInvalid)
			}
			if query.Error != nil {
				return nil, query.Error
			}
		case ScopeDimensionAll:
			// ALL 是显式的无限制范围，但仍允许与其它硬维度组合。
		default:
			return nil, fmt.Errorf("%w: dimension %d", ErrUnsupportedDimension, constraint.Dimension)
		}
	}

	if query.Error != nil {
		return nil, query.Error
	}
	return query, nil
}

func validateConstraints(constraints []RowConstraint) error {
	seen := make(map[uint32]struct{}, len(constraints))
	for index, constraint := range constraints {
		if constraint.Dimension == 0 {
			return fmt.Errorf("%w: constraint %d has unspecified dimension", ErrDecisionInvalid, index)
		}
		if _, duplicate := seen[constraint.Dimension]; duplicate {
			return fmt.Errorf("%w: duplicate dimension %d", ErrDecisionInvalid, constraint.Dimension)
		}
		seen[constraint.Dimension] = struct{}{}
		if constraint.Dimension == ScopeDimensionAll {
			if len(constraint.Refs) != 0 || constraint.IncludeDescendants || strings.TrimSpace(constraint.RelationKey) != "" {
				return fmt.Errorf("%w: all dimension cannot contain refs or resolver options", ErrDecisionInvalid)
			}
			continue
		}
		if len(constraint.Refs) == 0 {
			return fmt.Errorf("%w: constraint %d has no refs", ErrDecisionInvalid, index)
		}
		seenRefs := make(map[string]struct{}, len(constraint.Refs))
		for _, ref := range constraint.Refs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				return fmt.Errorf("%w: constraint %d has empty ref", ErrDecisionInvalid, index)
			}
			if _, duplicate := seenRefs[ref]; duplicate {
				return fmt.Errorf("%w: constraint %d has duplicate ref", ErrDecisionInvalid, index)
			}
			seenRefs[ref] = struct{}{}
		}
		if constraint.Dimension != ScopeDimensionOrganization && constraint.IncludeDescendants {
			return fmt.Errorf("%w: descendants require organization dimension", ErrDecisionInvalid)
		}
		if constraint.Dimension == ScopeDimensionRelation && strings.TrimSpace(constraint.RelationKey) == "" {
			return fmt.Errorf("%w: relation key is required", ErrDecisionInvalid)
		}
		if constraint.Dimension != ScopeDimensionRelation && strings.TrimSpace(constraint.RelationKey) != "" {
			return fmt.Errorf("%w: relation key is only valid for relation dimension", ErrDecisionInvalid)
		}
	}
	return nil
}

func bindColumn(column clause.Column) clause.Column {
	return column
}

func requireColumn(column clause.Column, dimension string) error {
	if strings.TrimSpace(column.Name) == "" {
		return fmt.Errorf("%w: %s column is empty", ErrUnsupportedDimension, dimension)
	}
	if column.Raw {
		return fmt.Errorf("%w: %s column must not be raw", ErrUnsupportedDimension, dimension)
	}
	return nil
}

func stringValues(refs []string) []interface{} {
	values := make([]interface{}, len(refs))
	for index, ref := range refs {
		values[index] = strings.TrimSpace(ref)
	}
	return values
}

func cloneRefs(refs []string) []string {
	return append([]string(nil), refs...)
}
