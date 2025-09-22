// Package internal provides HTTP middleware for github-copilot-svcs.
package internal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// HTTP status code constants
const (
	statusServerError = 500
	statusClientError = 400
)

// LoggingResponseWriter wraps http.ResponseWriter to capture response data and status code.
type LoggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

// NewLoggingResponseWriter ...
func NewLoggingResponseWriter(w http.ResponseWriter) *LoggingResponseWriter {
	return &LoggingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		body:           bytes.NewBuffer(nil),
	}
}

// WriteHeader ...
func (lrw *LoggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *LoggingResponseWriter) Write(body []byte) (int, error) {
	// Write to both the original response and our buffer
	lrw.body.Write(body)
	return lrw.ResponseWriter.Write(body)
}

// Hijack ...
func (lrw *LoggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := lrw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// StatusCode ...
func (lrw *LoggingResponseWriter) StatusCode() int {
	return lrw.statusCode
}

// Body ...
func (lrw *LoggingResponseWriter) Body() []byte {
	return lrw.body.Bytes()
}

// LoggingMiddleware logs HTTP requests and responses, including status code and duration.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create logging response writer
		lrw := NewLoggingResponseWriter(w)

		// Read and store request body for logging (if reasonable size)
		var requestBody []byte
		if r.Body != nil && r.ContentLength > 0 && r.ContentLength < 1024*1024 { // Max 1MB for logging
			requestBody, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// Attempt to extract model field (if JSON body present and small enough)
		modelName := ""
		if len(requestBody) > 0 {
			var tmp struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(requestBody, &tmp); err == nil && tmp.Model != "" {
				modelName = tmp.Model
			}
		}

		// Log request
		if modelName != "" {
			Info("HTTP Request",
				"method", r.Method,
				"url", r.URL.String(),
				"model", modelName,
				"remote_addr", getClientIP(r),
				"user_agent", r.UserAgent(),
				"content_length", r.ContentLength,
				"has_body", len(requestBody) > 0,
			)
		} else {
			Info("HTTP Request",
				"method", r.Method,
				"url", r.URL.String(),
				"remote_addr", getClientIP(r),
				"user_agent", r.UserAgent(),
				"content_length", r.ContentLength,
				"has_body", len(requestBody) > 0,
			)
		}

		// Process request
		next.ServeHTTP(lrw, r)

		// Calculate duration
		duration := time.Since(start)

		// Determine log level based on status code
		statusCode := lrw.StatusCode()
		responseSize := len(lrw.Body())

		logArgs := []interface{}{
			"method", r.Method,
			"url", r.URL.String(),
			"status_code", statusCode,
			"duration_ms", duration.Milliseconds(),
			"response_size", responseSize,
			"remote_addr", getClientIP(r),
		}

		// Log response with appropriate level
		switch {
		case statusCode >= statusServerError:
			Error("HTTP Response", logArgs...)
		case statusCode >= statusClientError:
			Warn("HTTP Response", logArgs...)
		default:
			Info("HTTP Response", logArgs...)
		}

		// Log response body for debugging if it's small and there was an error
		if statusCode >= 400 && responseSize > 0 && responseSize < 1024 {
			Debug("HTTP Response Body", "body", string(lrw.Body()))
		}
	})
}

// RecoveryMiddleware ...
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				Error("HTTP Handler Panic",
					"error", err,
					"method", r.Method,
					"url", r.URL.String(),
					"remote_addr", getClientIP(r),
				)

				WriteInternalError(w)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware ...
func CORSMiddleware(config *Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Set CORS headers based on configuration
			if len(config.CORS.AllowedOrigins) > 0 {
				if containsOrigin(config.CORS.AllowedOrigins, origin) || containsOrigin(config.CORS.AllowedOrigins, "*") {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
			}

			if len(config.CORS.AllowedHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(config.CORS.AllowedHeaders, ", "))
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			// Handle preflight requests
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeadersMiddleware ...
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Security headers
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Only set HSTS for HTTPS requests
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// TimeoutMiddleware sets a timeout for HTTP requests using http.TimeoutHandler.
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.TimeoutHandler(next, timeout, "Request timeout")
	}
}

// Helper functions
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (proxy)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}

	// Check X-Real-IP header (nginx)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}

	// Fall back to RemoteAddr
	if ip, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return ip
	}

	return r.RemoteAddr
}

func containsOrigin(origins []string, origin string) bool {
	for _, o := range origins {
		if o == origin || o == "*" {
			return true
		}
	}
	return false
}
