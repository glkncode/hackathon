package logger_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"ai-assistant/internal/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func newTestLogger(t *testing.T, cfg logger.Config) (*zap.Logger, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	cfg.Writer = &output
	log, err := logger.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := log.Sync(); err != nil {
			t.Errorf("Sync: %v", err)
		}
	})
	return log, &output
}

func jsonEntries(t *testing.T, output string) []map[string]any {
	t.Helper()
	if output == "" || !strings.HasSuffix(output, "\n") {
		t.Fatalf("expected newline-terminated JSON records, got %q", output)
	}
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid JSON record %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestDevelopment(t *testing.T) {
	log, output := newTestLogger(t, logger.Config{
		Environment: "development", Level: "debug", ServiceName: "auth-service",
	})
	log.Debug("user authenticated", zap.String("user_id", "123"))
	text := output.String()
	for _, want := range []string{"DEBUG", "auth-service", "user authenticated", `"user_id": "123"`} {
		if !strings.Contains(text, want) {
			t.Errorf("console output missing %q: %q", want, text)
		}
	}
	if !regexp.MustCompile(`logger_test\.go:\d+`).MatchString(text) {
		t.Errorf("missing caller: %q", text)
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.SplitN(text, "\t", 2)[0]); err != nil {
		t.Errorf("missing or invalid timestamp: %v", err)
	}
	if json.Valid(output.Bytes()) {
		t.Error("development output should use the console encoder")
	}
}

func TestProduction(t *testing.T) {
	log, output := newTestLogger(t, logger.Config{
		Environment: "production", ServiceName: "document-service",
	})
	log.Info("document indexed\nmetadata only", zap.String("document_id", "123"),
		zap.Int("chunks", 3), zap.Bool("cached", true))
	log.Warn("indexing delayed")
	entries := jsonEntries(t, output.String())
	if len(entries) != 2 {
		t.Fatalf("expected two single-line JSON records, got %d", len(entries))
	}
	entry := entries[0]
	for key, want := range map[string]any{
		"level": "info", "service": "document-service", "message": "document indexed\nmetadata only",
		"document_id": "123", "chunks": float64(3), "cached": true,
	} {
		if entry[key] != want {
			t.Errorf("%s = %v, want %v", key, entry[key], want)
		}
	}
	timestamp, ok := entry["timestamp"].(string)
	if !ok {
		t.Fatal("timestamp must be a string")
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		t.Errorf("invalid ISO 8601 timestamp: %v", err)
	}
	caller, _ := entry["caller"].(string)
	if !regexp.MustCompile(`logger_test\.go:\d+$`).MatchString(caller) {
		t.Errorf("invalid caller: %q", caller)
	}
}

func TestLevels(t *testing.T) {
	for _, environment := range []string{"development", "production"} {
		for _, level := range []string{"debug", "info", "warn", "error"} {
			t.Run(environment+"/"+level, func(t *testing.T) {
				log, output := newTestLogger(t, logger.Config{Environment: environment, Level: level})
				threshold, err := zapcore.ParseLevel(level)
				if err != nil {
					t.Fatal(err)
				}
				for _, candidate := range []zapcore.Level{zapcore.DebugLevel, zapcore.InfoLevel, zapcore.WarnLevel, zapcore.ErrorLevel} {
					output.Reset()
					wantEnabled := candidate >= threshold
					if got := log.Core().Enabled(candidate); got != wantEnabled {
						t.Errorf("Enabled(%s) = %v, want %v", candidate, got, wantEnabled)
					}
					log.Log(candidate, "level probe")
					if got := output.Len() > 0; got != wantEnabled {
						t.Errorf("output for %s present = %v, want %v", candidate, got, wantEnabled)
					}
				}
			})
		}
	}
}

func TestNamedAndWith(t *testing.T) {
	log, output := newTestLogger(t, logger.Config{Environment: "production"})
	requestLog := log.Named("auth").Named("sessions").With(zap.String("request_id", "request-123"))
	requestLog.Info("user authenticated", zap.String("user_id", "123"))
	log.Info("base logger")
	entries := jsonEntries(t, output.String())
	if len(entries) != 2 {
		t.Fatalf("expected two entries, got %d", len(entries))
	}
	for key, want := range map[string]any{
		"logger": "auth.sessions", "request_id": "request-123", "user_id": "123", "service": "ai-assistant",
	} {
		if entries[0][key] != want {
			t.Errorf("%s = %v, want %v", key, entries[0][key], want)
		}
	}
	for _, key := range []string{"logger", "request_id", "user_id"} {
		if _, exists := entries[1][key]; exists {
			t.Errorf("derived field %q leaked into base logger", key)
		}
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, level := range []string{"trace", "warning", "fatal", "panic", "dpanic", "INFO", " info ", "invalid"} {
		t.Run("level/"+level, func(t *testing.T) {
			log, err := logger.New(logger.Config{Level: level})
			if err == nil || log != nil {
				t.Fatalf("expected nil logger and explicit error, got %v, %v", log, err)
			}
			if !strings.Contains(err.Error(), level) {
				t.Errorf("error must identify invalid level: %v", err)
			}
		})
	}
	t.Run("environment", func(t *testing.T) {
		log, err := logger.New(logger.Config{Environment: "staging"})
		if err == nil || log != nil {
			t.Fatalf("expected nil logger and error for invalid environment, got %v, %v", log, err)
		}
	})
}

func TestDefaults(t *testing.T) {
	log, output := newTestLogger(t, logger.Config{})
	log.Debug("hidden debug")
	if output.Len() != 0 {
		t.Fatal("default info level must filter debug")
	}
	log.Info("default message")
	for _, want := range []string{"INFO", "ai-assistant", "default message"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("default console output missing %q: %q", want, output.String())
		}
	}
}

func TestConfigIgnoresEnvironmentVariables(t *testing.T) {
	t.Setenv("LOG_LEVEL", "invalid")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("SERVICE_NAME", "external-service")
	log, output := newTestLogger(t, logger.Config{})
	log.Info("explicit config only")
	if !strings.Contains(output.String(), "ai-assistant") || json.Valid(output.Bytes()) {
		t.Errorf("environment variables affected config: %q", output.String())
	}
}

func TestProductionStacktrace(t *testing.T) {
	log, output := newTestLogger(t, logger.Config{Environment: "production"})
	log.Warn("warning")
	log.Error("failure", zap.Error(errors.New("index unavailable")))
	entries := jsonEntries(t, output.String())
	if len(entries) != 2 {
		t.Fatalf("expected two entries, got %d", len(entries))
	}
	if _, exists := entries[0]["stacktrace"]; exists {
		t.Error("warn should not include a stacktrace")
	}
	stacktrace, _ := entries[1]["stacktrace"].(string)
	if !strings.Contains(stacktrace, "TestProductionStacktrace") {
		t.Errorf("error must include a useful stacktrace: %q", stacktrace)
	}
	if entries[1]["error"] != "index unavailable" {
		t.Errorf("structured error missing: %v", entries[1])
	}
}

func TestDefaultOutputIsStdout(t *testing.T) {
	// Do not parallelize: temporarily redirects the process-wide stdout handle.
	output, err := os.CreateTemp(t.TempDir(), "stdout-*.log")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = output
	t.Cleanup(func() {
		os.Stdout = original
		if err := output.Close(); err != nil {
			t.Errorf("close captured stdout: %v", err)
		}
	})
	log, err := logger.New(logger.Config{Environment: "production"})
	if err != nil {
		t.Fatal(err)
	}
	log.Info("stdout output")
	if err := log.Sync(); err != nil {
		t.Fatalf("Sync regular file: %v", err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	entries := jsonEntries(t, string(data))
	if len(entries) != 1 || entries[0]["message"] != "stdout output" {
		t.Errorf("unexpected stdout records: %v", entries)
	}
}

type failingSyncWriter struct {
	bytes.Buffer
	err error
}

func (w *failingSyncWriter) Sync() error { return w.err }

func TestSyncPreservesErrors(t *testing.T) {
	want := errors.New("sink sync failed")
	log, err := logger.New(logger.Config{Writer: &failingSyncWriter{err: want}})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Sync(); !errors.Is(err, want) {
		t.Errorf("Sync = %v, want %v", err, want)
	}
}

func TestConcurrentWrites(t *testing.T) {
	log, output := newTestLogger(t, logger.Config{Environment: "production"})
	const workers = 16
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			log.With(zap.Int("worker", i)).Info("completed")
		})
	}
	wg.Wait()
	if entries := jsonEntries(t, output.String()); len(entries) != workers {
		t.Errorf("got %d records, want %d", len(entries), workers)
	}
}

func ExampleNew() {
	var output bytes.Buffer
	log, err := logger.New(logger.Config{
		Environment: "production",
		Level:       "info",
		ServiceName: "ai-assistant",
		Writer:      &output, // Omit in application code to write to stdout.
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer func() {
		if err := log.Sync(); err != nil {
			// Standard streams may reject fsync on some platforms. Inspect the
			// error at shutdown; report unexpected I/O failures without panic.
			fmt.Fprintf(os.Stderr, "logger sync: %v\n", err)
		}
	}()
	log.Named("auth").With(zap.String("request_id", "request-123")).
		Info("user authenticated", zap.String("user_id", "123"))
	// Output:
}
