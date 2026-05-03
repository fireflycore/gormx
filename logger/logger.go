package logger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
	loger "gorm.io/gorm/logger"
)

const (
	// ResultSuccess 表示成功执行 SQL 的结果标记
	ResultSuccess = "success"
)

// OperationLogger 表示操作日志。
type OperationLogger struct {
	Database  string `json:"database"`
	Statement string `json:"statement"`
	Result    string `json:"result"`
	Path      string `json:"path"`

	Duration uint64 `json:"duration"`

	Level uint32 `json:"level"`
	Type  uint32 `json:"type"`

	TraceId  string `json:"trace_id"`
	ParentId string `json:"parent_id"`

	UserId   string `json:"user_id"`
	AppId    string `json:"app_id"`
	TenantId string `json:"tenant_id"`

	ServiceAppId      string `json:"service_app_id"`
	ServiceInstanceId string `json:"service_instance_id"`
}

// UserContextFields 为用户上下文字段，用于记录用户信息。
type UserContextFields struct {
	UserId            string `json:"user_id"`
	AppId             string `json:"app_id"`
	TenantId          string `json:"tenant_id"`
	OrgIds            string `json:"org_ids"`
	RoleIds           string `json:"role_ids"`
	ServiceAppId      string `json:"service_app_id"`
	ServiceInstanceId string `json:"service_instance_id"`
}

// Normalize 为用户上下文字段设置默认值。
func (u *UserContextFields) Normalize() {
	if u.UserId == "" {
		u.UserId = "user-id"
	}
	if u.OrgIds == "" {
		u.OrgIds = "org-ids"
	}
	if u.RoleIds == "" {
		u.RoleIds = "role-ids"
	}

	if u.AppId == "" {
		u.AppId = "app-id"
	}
	if u.TenantId == "" {
		u.TenantId = "tenant-id"
	}

	if u.ServiceAppId == "" {
		u.ServiceAppId = "service-app-id"
	}
	if u.ServiceInstanceId == "" {
		u.ServiceInstanceId = "service-instance-id"
	}
}

// Config 为自定义 gorm logger 的配置
type Config struct {
	// Config 复用 gorm 内置 logger.Config（包含 SlowThreshold/LogLevel/Colorful 等）
	loger.Config

	// 控制台是否输出日志
	Console bool
	// 数据库
	Database string
	// 数据库类型
	DatabaseType uint32
}

// NewLogger 构造一个 gorm logger，实现控制台输出与自定义回调输出
func NewLogger(config Config, userContextFields *UserContextFields) loger.Interface {
	// 定义各级别日志输出模板（可选彩色）
	var (
		infoStr      = "[%s] [info] [Database:%s]\n%s\n%s"
		warnStr      = "[%s] [warn] [Database:%s]\n%s\n%s"
		errStr       = "[%s] [error] [Database:%s]\n%s\n%s"
		traceStr     = "[%s] [info] [Database:%s] [Rows:%v] [Duration:%.3fms] [Path:%s]\n%s"
		traceWarnStr = "[%s] [warn] [Database:%s] [Rows:%v] [Duration:%.3fms]	[Path:%s]\n%s\n%s"
		traceErrStr  = "[%s] [error] [Database:%s] [Rows:%v] [Duration:%.3fms] [Path:%s]\n%s\n%s"
	)

	// 若开启彩色输出，则替换模板为 gorm logger 预置的 ANSI 颜色字符串
	if config.Colorful {
		// date, level, db, file, msg
		infoStr = loger.BlueBold + "[%s] " + loger.BlueBold + "[info] " + loger.BlueBold + "[Database:%s]\n" + loger.Green + "%s\n" + loger.Reset + "%s"
		warnStr = loger.YellowBold + "[%s] " + loger.YellowBold + "[warn] " + loger.BlueBold + "[Database:%s]\n" + loger.Green + "%s\n" + loger.Yellow + "%s\n" + loger.Reset
		errStr = loger.RedBold + "[%s] " + loger.RedBold + "[error] " + loger.BlueBold + "[Database:%s]\n" + loger.Green + "%s\n" + loger.Red + "%s\n" + loger.Reset

		// date, level, db, rows, timer, file, sql
		traceStr = loger.BlueBold + "[%s] " + loger.BlueBold + "[info] " + loger.BlueBold + "[Database:%s] " + loger.YellowBold + "[Rows:%v]" + loger.Yellow + " [Duration:%.3fms]" + loger.Green + " [Path:%s]\n" + loger.Reset + "%s"
		// date, level, db, rows, timer, file, slowLog, sql
		traceWarnStr = loger.YellowBold + "[%s] " + loger.YellowBold + "[warn] " + loger.BlueBold + "[Database:%s] " + loger.YellowBold + "[Rows:%v]" + loger.Yellow + " [Duration:%.3fms]" + loger.Green + " [Path:%s]\n" + loger.Yellow + "%s\n" + loger.Reset + "%s"
		// date, level, db, rows, timer, file, err, sql
		traceErrStr = loger.RedBold + "[%s] " + loger.RedBold + "[error] " + loger.BlueBold + "[Database:%s] " + loger.YellowBold + "[Rows:%v]" + loger.Yellow + " [Duration:%.3fms]" + loger.Green + " [Path:%s]\n" + loger.Red + "%s\n" + loger.Reset + "%s"
	}

	// 返回实现 loger.Interface 的 logger 实例
	return &logger{
		// 复用 gorm logger 配置
		Config: config.Config,
		// 保存各级别输出模板
		infoStr:      infoStr,
		warnStr:      warnStr,
		errStr:       errStr,
		traceStr:     traceStr,
		traceWarnStr: traceWarnStr,
		traceErrStr:  traceErrStr,
		// console 控制是否输出到控制台
		console: config.Console,
		// database 记录库名便于聚合检索
		database: config.Database,
		// databaseType 记录库类型便于聚合检索
		databaseType: config.DatabaseType,
		// userContextFields 记录用户上下文字段便于聚合检索
		userContextFields: userContextFields,
	}
}

// logger 为内部实现，满足 gorm 的 logger.Interface
type logger struct {
	// Config 为 gorm logger 配置（嵌入以复用字段）
	loger.Config
	// infoStr/warnStr/errStr 为普通日志模板
	infoStr, warnStr, errStr string
	// traceStr/traceErrStr/traceWarnStr 为 SQL Trace 日志模板
	traceStr, traceErrStr, traceWarnStr string
	// database 为库名
	database string
	// databaseType 为库类型
	databaseType uint32
	// console 控制台输出开关
	console bool
	// 用户上下文字段
	userContextFields *UserContextFields
}

// LogMode 设置日志级别，返回一个新的 logger（符合 gorm 约定）
func (l *logger) LogMode(level loger.LogLevel) loger.Interface {
	// 复制一份，避免修改原实例带来的并发问题
	newLogger := *l
	// 更新日志级别
	newLogger.LogLevel = level
	// 返回新实例
	return &newLogger
}

// Info 输出 info 日志（受 LogLevel 与 console 开关控制）
func (l *logger) Info(_ context.Context, msg string, data ...interface{}) {
	// 仅当启用控制台输出且 LogLevel 允许时才输出
	if l.console && l.LogLevel >= loger.Info {
		date := time.Now().Format(time.DateTime)
		file := fileWithLineNum()
		// msg 和 data 组合成完整消息
		fullMsg := fmt.Sprintf(msg, data...)
		// 输出到标准输出: date, db, file, msg
		fmt.Printf(l.infoStr+"\n", date, l.database, file, fullMsg)
	}
}

// Warn 输出 warn 日志（受 LogLevel 与 console 开关控制）
func (l *logger) Warn(_ context.Context, msg string, data ...interface{}) {
	// 仅当启用控制台输出且 LogLevel 允许时才输出
	if l.console && l.LogLevel >= loger.Warn {
		date := time.Now().Format(time.DateTime)
		file := fileWithLineNum()
		fullMsg := fmt.Sprintf(msg, data...)
		// 输出到标准输出: date, db, file, msg
		fmt.Printf(l.warnStr+"\n", date, l.database, file, fullMsg)
	}
}

// Error 输出 error 日志（受 LogLevel 与 console 开关控制）
func (l *logger) Error(_ context.Context, msg string, data ...interface{}) {
	// 仅当启用控制台输出且 LogLevel 允许时才输出
	if l.console && l.LogLevel >= loger.Error {
		date := time.Now().Format(time.DateTime)
		file := fileWithLineNum()
		fullMsg := fmt.Sprintf(msg, data...)
		// 输出到标准输出: date, db, file, msg
		fmt.Printf(l.errStr+"\n", date, l.database, file, fullMsg)
	}
}

// Trace 记录 SQL 执行信息（成功/慢 SQL/错误）
func (l *logger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	// Silent 模式不输出任何日志
	if l.LogLevel <= loger.Silent {
		return
	}

	// elapsed 为 SQL 执行耗时
	elapsed := time.Since(begin)
	// 按错误/慢 SQL/普通 SQL 分支处理
	switch {
	case err != nil && l.LogLevel >= loger.Error && (!errors.Is(err, loger.ErrRecordNotFound) || !l.IgnoreRecordNotFoundError):
		// 从回调取出 SQL 文本与影响行数
		sql, rows := fc()
		// timer 为耗时的毫秒值（浮点便于输出 3 位小数）
		timer := float64(elapsed.Nanoseconds()) / 1e6
		// file 为调用位置
		file := fileWithLineNum()
		date := time.Now().Format(time.DateTime)

		// 控制台输出（若开启）
		if l.console {
			rowsStr := "-"
			if rows != -1 {
				rowsStr = fmt.Sprintf("%v", rows)
			}
			// traceErrStr expects: date, db, rows, timer, file, err, sql
			fmt.Printf(l.traceErrStr+"\n", date, l.database, rowsStr, timer, file, err, sql)
		}
		// 结构化回调输出
		l.handleLog(ctx, loger.Error, file, sql, err.Error(), elapsed)

	case elapsed > l.SlowThreshold && l.SlowThreshold != 0 && l.LogLevel >= loger.Warn:
		// 从回调取出 SQL 文本与影响行数
		sql, rows := fc()
		// slowLog 为慢 SQL 标记文本
		slowLog := fmt.Sprintf("SLOW SQL >= %v", l.SlowThreshold)
		// timer 为耗时的毫秒值（浮点便于输出 3 位小数）
		timer := float64(elapsed.Nanoseconds()) / 1e6
		// file 为调用位置
		file := fileWithLineNum()
		date := time.Now().Format(time.DateTime)

		// 控制台输出（若开启）
		if l.console {
			rowsStr := "-"
			if rows != -1 {
				rowsStr = fmt.Sprintf("%v", rows)
			}
			// traceWarnStr expects: date, db, rows, timer, file, slowLog, sql
			fmt.Printf(l.traceWarnStr+"\n", date, l.database, rowsStr, timer, file, slowLog, sql)
		}
		// 结构化回调输出
		l.handleLog(ctx, loger.Warn, file, sql, slowLog, elapsed)

	case l.LogLevel == loger.Info:
		// 从回调取出 SQL 文本与影响行数
		sql, rows := fc()
		// timer 为耗时的毫秒值（浮点便于输出 3 位小数）
		timer := float64(elapsed.Nanoseconds()) / 1e6
		// file 为调用位置
		file := fileWithLineNum()
		date := time.Now().Format(time.DateTime)

		// 控制台输出（若开启）
		if l.console {
			rowsStr := "-"
			if rows != -1 {
				rowsStr = fmt.Sprintf("%v", rows)
			}
			// traceStr expects: date, db, rows, timer, file, sql
			fmt.Printf(l.traceStr+"\n", date, l.database, rowsStr, timer, file, sql)
		}
		// 结构化回调输出（成功分支）
		l.handleLog(ctx, loger.Info, file, sql, ResultSuccess, elapsed)
	}
}

// handleLog 将日志以 JSON 形式写入回调（若提供）
func (l *logger) handleLog(ctx context.Context, level loger.LogLevel, path, smt, result string, elapsed time.Duration) {
	// log 为结构化日志内容，字段名保持相对稳定便于下游解析
	logData := &OperationLogger{
		Database:  l.database,
		Statement: smt,
		Result:    result,
		Path:      path,

		Duration: uint64(elapsed.Microseconds()),

		Level: levelConvertValue(level),
		Type:  l.databaseType,
	}

	// 从 OTel span context 中提取链路字段（优先）
	spanCtx := trace.SpanFromContext(ctx).SpanContext()
	if spanCtx.IsValid() {
		logData.TraceId = spanCtx.TraceID().String()
		logData.ParentId = spanCtx.SpanID().String()
	}

	// 从 gRPC metadata 中提取链路字段（存在则写入结构化日志，作为兼容兜底）
	md, _ := metadata.FromIncomingContext(ctx)
	if gd := md.Get(l.userContextFields.UserId); len(gd) != 0 {
		logData.UserId = gd[0]
	}
	if gd := md.Get(l.userContextFields.AppId); len(gd) != 0 {
		logData.AppId = gd[0]
	}
	if gd := md.Get(l.userContextFields.TenantId); len(gd) != 0 {
		logData.TenantId = gd[0]
	}

	if gd := md.Get(l.userContextFields.ServiceAppId); len(gd) != 0 {
		logData.ServiceAppId = gd[0]
	}
	if gd := md.Get(l.userContextFields.ServiceInstanceId); len(gd) != 0 {
		logData.ServiceInstanceId = gd[0]
	}

	l.emitOTelOperationLog(ctx, level, logData)
}

func (l *logger) emitOTelOperationLog(ctx context.Context, level loger.LogLevel, logData *OperationLogger) {
	if logData == nil {
		return
	}

	otelLogger := global.Logger("gormx")

	var record log.Record
	record.SetTimestamp(time.Now())
	record.SetSeverity(convertOTelSeverity(level))
	record.SetSeverityText(convertOTelSeverityText(level))

	if b, err := json.Marshal(logData); err == nil {
		record.SetBody(log.StringValue(string(b)))
	} else {
		record.SetBody(log.StringValue(logData.Statement))
	}

	record.AddAttributes(
		log.String("log_type", "operation"),
		log.String("database", logData.Database),
		log.String("statement", logData.Statement),
		log.String("result", logData.Result),
		log.String("path", logData.Path),
		log.Int64("duration", int64(logData.Duration)),
		log.Int64("db_type", int64(logData.Type)),
	)
	if logData.UserId != "" {
		record.AddAttributes(log.String("user_id", logData.UserId))
	}
	if logData.AppId != "" {
		record.AddAttributes(log.String("app_id", logData.AppId))
	}
	if logData.TenantId != "" {
		record.AddAttributes(log.String("tenant_id", logData.TenantId))
	}

	otelLogger.Emit(ctx, record)
}

func convertOTelSeverity(level loger.LogLevel) log.Severity {
	switch level {
	case loger.Error:
		return log.SeverityError
	case loger.Warn:
		return log.SeverityWarn
	case loger.Info:
		return log.SeverityInfo
	default:
		return log.SeverityUndefined
	}
}

func convertOTelSeverityText(level loger.LogLevel) string {
	switch level {
	case loger.Error:
		return "ERROR"
	case loger.Warn:
		return "WARN"
	case loger.Info:
		return "INFO"
	default:
		return ""
	}
}

func fileWithLineNum() string {
	for i := 2; i < 15; i++ {
		_, file, line, ok := runtime.Caller(i)
		if ok && (!strings.Contains(file, "gorm.io/gorm") || strings.HasSuffix(file, "_test.go")) && !strings.Contains(file, "gormx/internal/logger.go") {
			return file + ":" + strconv.FormatInt(int64(line), 10)
		}
	}
	return ""
}

func levelConvertValue(level loger.LogLevel) uint32 {
	switch level {
	case loger.Info:
		return 1
	case loger.Warn:
		return 2
	case loger.Error:
		return 3
	default:
		return 0
	}
}
