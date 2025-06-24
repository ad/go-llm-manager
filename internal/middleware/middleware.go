package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CORS middleware
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Logging middleware
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Подсчет длины заголовков
		headersLen := 0
		for k, v := range r.Header {
			headersLen += len(k)
			for _, vv := range v {
				headersLen += len(vv)
			}
		}

		// Подсчет длины тела
		var bodyLen int
		if r.ContentLength > 0 {
			bodyLen = int(r.ContentLength)
		} else {
			// Если ContentLength неизвестен, читаем body вручную (но не изменяем r.Body для хендлеров)
			// Можно реализовать через io.TeeReader, если нужно точное значение
		}

		// Обертка для захвата кода ответа
		rw := &loggingResponseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(rw, r)
		duration := time.Since(start)
		// ua := r.Header.Get("User-Agent")
		ip := r.RemoteAddr
		if ipHeader := r.Header.Get("X-Real-IP"); ipHeader != "" {
			ip = ipHeader
		} else if ipHeader := r.Header.Get("X-Forwarded-For"); ipHeader != "" {
			ip = ipHeader
		}
		logLine := fmt.Sprintf("[REQ] %s %s from %s | %v | code=%d | headers=%dB | body=%dB", r.Method, r.URL.Path, ip, duration, rw.statusCode, headersLen, bodyLen)
		println(logLine)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode        int
	writeHeaderCalled bool
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	if lrw.writeHeaderCalled {
		return
	}
	lrw.statusCode = code
	lrw.writeHeaderCalled = true
	lrw.ResponseWriter.WriteHeader(code)
}

// Content type middleware
func ContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set default content type
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		next.ServeHTTP(w, r)
	})
}

// Chain middleware
func Chain(h http.Handler, middlewares ...func(http.Handler) http.Handler) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

// Rate limiting middleware
func RateLimit(checker func(userID string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract user ID from JWT or request
			userID := extractUserID(r)
			if userID != "" && !checker(userID) {
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error": "Rate limit exceeded"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Helper to extract user ID from request
func extractUserID(r *http.Request) string {
	// Try to get from JWT token first
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		// TODO: Decode JWT and extract user_id
		return ""
	}

	// Fallback to query parameter
	return r.URL.Query().Get("user_id")
}
