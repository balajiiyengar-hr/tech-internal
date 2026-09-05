package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tech-internal/internal/fmtlog"
)

func main() {
	port := getenv("UI_PORT", "3100")
	staticDir := getenv("UI_DIR", "./frontend")
	apiBase := strings.TrimRight(getenv("API_BASE_URL", "http://127.0.0.1:8080/api/v1"), "/")
	fmtlog.Init(getenv("SERVICE_NAME", "tech-internal-ui"), getenv("APP_ENV", "dev"))

	absDir, err := filepath.Abs(staticDir)
	if err != nil {
		fmtlog.Fatal("startup_failed", map[string]any{"error.message": err.Error(), "event.action": "ui_dir"})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/assets/js/config.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = fmt.Fprintf(w, "window.PORTAL_CONFIG = { apiBase: %q };\n", apiBase)
	})
	servePage := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(absDir, name))
		}
	}
	mux.HandleFunc("/login", servePage("index.html"))
	mux.HandleFunc("/admin/login", servePage("admin-login.html"))
	mux.Handle("/", http.FileServer(http.Dir(absDir)))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           accessLog(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	fmtlog.Info("server_started", map[string]any{
		"event.action": "listen",
		"server.port":  port,
		"url.original": apiBase,
	})
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmtlog.Fatal("server_failed", map[string]any{"error.message": err.Error(), "event.action": "listen"})
	}
}

func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" {
			id = fmtlog.NewRequestID()
		}
		rw := &statusWriter{ResponseWriter: w, status: 200}
		rw.Header().Set("X-Request-ID", id)
		next.ServeHTTP(rw, r)

		status := rw.status
		if status < 400 && (strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.ico") {
			return
		}
		level := "info"
		if status >= 500 {
			level = "error"
		} else if status >= 400 {
			level = "warn"
		}
		fmtlog.Default().Log(level, "http_request", map[string]any{
			"trace.id":                  id,
			"http.request.id":           id,
			"http.request.method":       r.Method,
			"url.path":                  r.URL.Path,
			"http.response.status_code": status,
			"event.duration":            time.Since(start).Nanoseconds(),
			"event.action":              "http_request",
			"client.ip":                 r.RemoteAddr,
		})
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
