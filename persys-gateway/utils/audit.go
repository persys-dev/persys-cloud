package utils

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/persys-dev/persys-cloud/api-gateway/config" // Adjust import based on actual config package
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger wraps a Zap logger with Persys Cloud-specific configuration.
type Logger struct {
	logger *zap.Logger
	config *config.Config
}

// NewLogger initializes a Zap logger with Persys Cloud settings.
func NewLogger(cfg *config.Config) (*Logger, error) {
	// Configure log level
	var level zapcore.Level
	switch cfg.Log.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		level = zapcore.InfoLevel
	}

	// Configure encoder
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	// Configure core
	var cores []zapcore.Core
	stdoutCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.Lock(os.Stdout),
		zap.NewAtomicLevelAt(level),
	)
	cores = append(cores, stdoutCore)

	// Optional: Loki sink (if configured)
	if cfg.Log.LokiEndpoint != "" {
		lokiSink, err := newLokiSink(cfg.Log.LokiEndpoint, "api-gateway")
		if err != nil {
			return nil, fmt.Errorf("failed to create Loki sink: %w", err)
		}
		lokiCore := zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderCfg),
			lokiSink,
			zap.NewAtomicLevelAt(level),
		)
		cores = append(cores, lokiCore)
	}

	// Combine cores
	core := zapcore.NewTee(cores...)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	// Add service field
	logger = logger.With(zap.String("service", "api-gateway"))

	return &Logger{
		logger: logger,
		config: cfg,
	}, nil
}

// Close flushes and closes the logger.
func (l *Logger) Close() error {
	return l.logger.Sync()
}

// LogInfo logs an info-level message.
func (l *Logger) LogInfo(ctx context.Context, msg string, fields ...zap.Field) {
	l.logWithContext(ctx, zapcore.InfoLevel, msg, fields...)
}

// LogWarn logs a warning-level message.
func (l *Logger) LogWarn(ctx context.Context, msg string, fields ...zap.Field) {
	l.logWithContext(ctx, zapcore.WarnLevel, msg, fields...)
}

// LogError logs an error-level message with an optional error.
func (l *Logger) LogError(ctx context.Context, msg string, err error, fields ...zap.Field) {
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	l.logWithContext(ctx, zapcore.ErrorLevel, msg, fields...)
}

// LogDebug logs a debug-level message.
func (l *Logger) LogDebug(ctx context.Context, msg string, fields ...zap.Field) {
	l.logWithContext(ctx, zapcore.DebugLevel, msg, fields...)
}

// logWithContext adds trace ID and logs the message.
func (l *Logger) logWithContext(ctx context.Context, level zapcore.Level, msg string, fields ...zap.Field) {
	// Extract trace ID from OpenTelemetry context
	if span := trace.SpanFromContext(ctx); span.IsRecording() {
		traceID := span.SpanContext().TraceID().String()
		fields = append(fields, zap.String("trace_id", traceID))
	}

	// Add timestamp
	fields = append(fields, zap.Time("timestamp", time.Now()))

	switch level {
	case zapcore.DebugLevel:
		l.logger.Debug(msg, fields...)
	case zapcore.InfoLevel:
		l.logger.Info(msg, fields...)
	case zapcore.WarnLevel:
		l.logger.Warn(msg, fields...)
	case zapcore.ErrorLevel:
		l.logger.Error(msg, fields...)
	}
}

// newLokiSink creates a Zap sink for Loki (placeholder implementation).
func newLokiSink(endpoint, serviceName string) (zapcore.WriteSyncer, error) {
	// Implement Loki client (e.g., using github.com/grafana/loki-client-go)
	// Placeholder: return stdout as fallback
	return zapcore.Lock(os.Stdout), nil
}
