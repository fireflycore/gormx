package gormx

import (
	"time"

	"github.com/fireflycore/gormx/logger"
	loger "gorm.io/gorm/logger"
)

// NewLogger 根据 Config 构造 gorm logger。
func NewLogger(c *Config) loger.Interface {
	// c 为空或未开启 Logger 时，直接丢弃日志输出。
	if c == nil || !c.Logger {
		// Discard 为 gorm 提供的空实现。
		return loger.Discard
	}

	// 初始化 UserContextFields。
	if c.userContextFields == nil {
		c.userContextFields = &logger.UserContextFields{}
		c.userContextFields.Normalize()
	}

	// internal.New 返回一个实现 loger.Interface 的自定义 logger。
	return logger.NewLogger(logger.Config{
		// Config 复用 gorm 自带的 logger.Config。
		Config: loger.Config{
			// SlowThreshold 为慢 SQL 阈值。
			SlowThreshold: 200 * time.Millisecond,
			// LogLevel 使用 Info，输出 SQL Trace。
			LogLevel: loger.Info,
			// Colorful 控制控制台彩色输出。
			Colorful: true,
		},
		// Console 控制是否输出到控制台。
		Console: c.loggerConsole,
		// Database 记录库名，便于日志聚合。
		Database: c.Database,
		// DatabaseType 记录库类型，便于日志聚合。
		DatabaseType: c.Type,
	}, c.userContextFields)
}
