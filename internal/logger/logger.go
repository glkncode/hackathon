// Package logger constructs loggers for injection into application services.
//
// Log identifiers and operational metadata, not passwords, session tokens,
// cookies, Authorization headers, connection secrets, document contents, or
// confidential LLM prompts. Fields are not automatically redacted.
package logger

import (
	"fmt"
	"io"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config is supplied by the caller, independently of environment variables.
// Empty Environment, Level, and ServiceName use development, info, and
// ai-assistant respectively.
type Config struct {
	Environment string
	Level       string
	ServiceName string

	// Writer optionally replaces stdout, for example with a test buffer.
	// The logger serializes writes and does not close the writer. If Writer
	// implements Sync() error, logger.Sync forwards to it.
	Writer io.Writer
}

// New creates a logger once at application startup. Services can share it or
// derive contextual loggers using Named and With.
//
// The caller owns shutdown and should call Sync before exiting. Sync errors
// are returned unchanged: stdout/stderr may report platform-specific errors
// when fsync is unsupported. Handle those at the application boundary without
// panicking or discarding unrelated I/O errors.
func New(cfg Config) (*zap.Logger, error) {
	if cfg.Environment == "" {
		cfg.Environment = "development"
	}
	if cfg.Level == "" {
		cfg.Level = "info"
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "ai-assistant"
	}

	var level zapcore.Level
	switch cfg.Level {
	case "debug":
		level = zapcore.DebugLevel
	case "info":
		level = zapcore.InfoLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	default:
		return nil, fmt.Errorf("logger: invalid level %q (expected debug, info, warn, or error)", cfg.Level)
	}

	var encoderConfig zapcore.EncoderConfig
	switch cfg.Environment {
	case "development":
		encoderConfig = zap.NewDevelopmentEncoderConfig()
	case "production":
		encoderConfig = zap.NewProductionEncoderConfig()
	default:
		return nil, fmt.Errorf("logger: invalid environment %q (expected development or production)", cfg.Environment)
	}
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.LevelKey = "level"
	encoderConfig.NameKey = "logger"
	encoderConfig.CallerKey = "caller"
	encoderConfig.MessageKey = "message"
	encoderConfig.StacktraceKey = "stacktrace"
	encoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder

	var encoder zapcore.Encoder
	if cfg.Environment == "production" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	writer := cfg.Writer
	if writer == nil {
		writer = os.Stdout
	}
	core := zapcore.NewCore(encoder, zapcore.Lock(zapcore.AddSync(writer)), level)
	return zap.New(core,
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.Fields(zap.String("service", cfg.ServiceName)),
	), nil
}
