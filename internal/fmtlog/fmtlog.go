package fmtlog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

const ECSVersion = "8.11.0"

type Logger struct {
	mu      sync.Mutex
	out     io.Writer
	service string
	env     string
}

var defaultLogger = New(os.Stdout, "tech-internal-api", "dev")

func Init(service, env string) {
	if service == "" {
		service = "tech-internal-api"
	}
	if env == "" {
		env = "dev"
	}
	defaultLogger = New(os.Stdout, service, env)
}

func New(out io.Writer, service, env string) *Logger {
	if out == nil {
		out = os.Stdout
	}
	return &Logger{out: out, service: service, env: env}
}

func Default() *Logger {
	return defaultLogger
}

func Info(message string, fields map[string]any)  { defaultLogger.Log("info", message, fields) }
func Warn(message string, fields map[string]any)  { defaultLogger.Log("warn", message, fields) }
func Error(message string, fields map[string]any) { defaultLogger.Log("error", message, fields) }

func Fatal(message string, fields map[string]any) {
	defaultLogger.Log("fatal", message, fields)
	os.Exit(1)
}

func (l *Logger) Log(level, message string, fields map[string]any) {
	line := map[string]any{
		"@timestamp":           time.Now().UTC().Format(time.RFC3339Nano),
		"ecs.version":          ECSVersion,
		"log.level":            level,
		"message":              message,
		"service.name":         l.service,
		"service.environment":  l.env,
		"event.dataset":        l.service + "." + message,
	}
	for k, v := range fields {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		line[k] = v
	}

	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	b = append(b, '\n')

	l.mu.Lock()
	_, _ = l.out.Write(b)
	l.mu.Unlock()
}

func NewRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405.000")))
	}
	return hex.EncodeToString(buf[:])
}
