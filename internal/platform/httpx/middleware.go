package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
)

type ctxKey int

const requestIDKey ctxKey = iota

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap открывает ResponseController доступ к Flush исходного писателя:
// без него потоковая отдача копилась бы в буфере сервера.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Log пишет ОДНУ строку на запрос и не логирует тела и заголовки:
// редакция секретов в логгере есть, но самый надёжный способ не утечь —
// не собирать (ADR-0004, ADR-0005).
func Log(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			log.Info("http",
				"method", r.Method, "path", r.URL.Path, "status", sw.status,
				"bytes", sw.bytes, "dur_ms", time.Since(start).Milliseconds(),
				"rid", RequestIDFrom(r.Context()))
		})
	}
}

func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						// Штатный обрыв ответа посреди тела, не сбой: сервер
						// рвёт соединение, и клиент не примет обрубок за целое.
						// Ответ «internal» дописался бы в чужое тело.
						panic(rec)
					}
					log.Error("panic", "err", rec, "path", r.URL.Path,
						"stack", string(debug.Stack()))
					JSON(w, http.StatusInternalServerError, ErrorBody{Error: "internal"})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
