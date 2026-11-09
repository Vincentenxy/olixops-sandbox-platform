// Package logger 提供全局统一的日志打印能力，基于 zap 封装。
//
// 规范要求：
//   - 全局日志只允许使用本模块，禁止直接使用 fmt.Println / log.Println / zap 全局 Logger
//   - 支持 trace-id 透传：HTTP 请求自动透传 x-request-id
//   - 日志级别带颜色输出（info 绿色、error 红色等），格式人类可读
package logger

import (
	"context"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// contextKey 自定义 context key 类型，避免冲突
type contextKey string

// TraceIDKey 是 context 中存储 trace-id 的 key
const TraceIDKey contextKey = "traceId"

// base 为基础 logger（未加 caller skip 偏移），供两种包装方式复用
var base *zap.Logger

// sugared 为带偏移的全局 logger，供 Debug/Info/Warn/Error 包级函数使用
var sugared *zap.Logger

// Init 初始化全局日志
// mode: debug / release / test（与 gin 模式一致，debug 下输出到控制台且带颜色）
func Init(mode string) {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:       "time",
		LevelKey:      "level",
		NameKey:       "logger",
		CallerKey:     "caller",
		MessageKey:    "msg",
		StacktraceKey: "stacktrace",
		LineEnding:    zapcore.DefaultLineEnding,
		EncodeTime: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02T15:04:05.000-07:00"))
		},
		EncodeCaller: zapcore.ShortCallerEncoder,
	}

	// debug 模式：彩色级别 + 人类可读格式
	// release 模式：JSON 格式（生产环境便于日志采集）
	var core zapcore.Core
	if mode == "debug" || mode == "test" {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		core = zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			zap.NewAtomicLevelAt(zap.DebugLevel),
		)
	} else {
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			zap.NewAtomicLevelAt(zap.InfoLevel),
		)
	}

	base = zap.New(core, zap.AddCaller())
	sugared = base.WithOptions(zap.AddCallerSkip(2))
}

// L 返回全局 Logger（无 trace-id 场景使用，如启动阶段）
// 返回的是未加偏移的实例，调用方位置准确
func L() *zap.Logger {
	if base == nil {
		// 未初始化时兜底，防止 nil panic
		base = zap.NewNop()
	}
	return base
}

// traceField 从 context 中提取 trace-id 并组装为 zap 字段
// 支持 gin.Context 和标准 context.Context；无 trace-id 时返回 nil
func traceField(ctx context.Context) *zap.Field {
	if ctx == nil {
		return nil
	}

	var traceID string
	if gc, ok := ctx.(*gin.Context); ok {
		traceID = gc.GetString(string(TraceIDKey))
	} else {
		traceID, _ = ctx.Value(TraceIDKey).(string)
	}

	if traceID == "" {
		return nil
	}
	f := zap.String("traceId", traceID)
	return &f
}

// Debug 打印 debug 级别日志（带 trace-id）
func Debug(ctx context.Context, msg string, fields ...zap.Field) {
	log(ctx, zap.DebugLevel, msg, fields...)
}

// Info 打印 info 级别日志（带 trace-id）
func Info(ctx context.Context, msg string, fields ...zap.Field) {
	log(ctx, zap.InfoLevel, msg, fields...)
}

// Warn 打印 warn 级别日志（带 trace-id）
func Warn(ctx context.Context, msg string, fields ...zap.Field) {
	log(ctx, zap.WarnLevel, msg, fields...)
}

// Error 打印 error 级别日志（带 trace-id）
func Error(ctx context.Context, msg string, fields ...zap.Field) {
	log(ctx, zap.ErrorLevel, msg, fields...)
}

// log 统一输出：有 trace-id 时附加，无时省略（占位符规则：有才打印）
func log(ctx context.Context, level zapcore.Level, msg string, fields ...zap.Field) {
	if f := traceField(ctx); f != nil {
		fields = append([]zap.Field{*f}, fields...)
	}
	if ce := sugared.Check(level, msg); ce != nil {
		ce.Write(fields...)
	}
}

// Sync 刷新日志缓冲，程序退出前调用
func Sync() {
	if base != nil {
		_ = base.Sync()
	}
}
