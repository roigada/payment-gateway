package httpapi

import (
	"context"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/roigada/payment-gateway/internal/serviceauth"
)

type principalContextKey struct{}

func (s *Handler) requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		credential, ok := bearerCredential(r.Header.Get("Authorization"))
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized", http.StatusText(http.StatusUnauthorized))
			return
		}
		principal, ok := s.authenticator.Authenticate(credential)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "unauthorized", http.StatusText(http.StatusUnauthorized))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}

func bearerCredential(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func (s *Handler) requireScope(scope serviceauth.Scope, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := r.Context().Value(principalContextKey{}).(serviceauth.Principal)
		if !ok || !principal.HasScope(scope) {
			writeError(w, http.StatusForbidden, "forbidden", http.StatusText(http.StatusForbidden))
			return
		}
		next(w, r)
	}
}

func (s *Handler) limitRate(routeClass RouteClass, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := r.Context().Value(principalContextKey{}).(serviceauth.Principal)
		if !ok {
			writeError(w, http.StatusForbidden, "forbidden", http.StatusText(http.StatusForbidden))
			return
		}
		allowed, retryAfter := s.rateLimiter.Reserve(principal.ID(), routeClass)
		if !allowed {
			s.metrics.RecordRateLimitRejection(string(routeClass))
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Handler) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &responseRecorder{ResponseWriter: w}

		defer func() {
			if err := recover(); err != nil {
				if err == http.ErrAbortHandler {
					panic(err)
				}

				s.logger.Error("http panic recovered",
					"method", r.Method,
					"uri", r.URL.RequestURI(),
					"remote_addr", r.RemoteAddr,
					"proto", r.Proto,
					"user_agent", r.UserAgent(),
					"panic_value", err,
					"stack", string(debug.Stack()),
				)
				rec.Header().Set("Connection", "close")
				if !rec.wroteHeader {
					writeError(rec, http.StatusInternalServerError, errorCodeInternalServer, http.StatusText(http.StatusInternalServerError))
				}
			}
		}()

		next.ServeHTTP(rec, r)
	})
}

func (s *Handler) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		rec := &responseRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		attrs := []any{
			"method", r.Method,
			"uri", r.URL.RequestURI(),
			"status", status,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"remote_addr", r.RemoteAddr,
			"proto", r.Proto,
			"user_agent", r.UserAgent(),
		}
		if status >= http.StatusInternalServerError {
			s.logger.Error("http request", attrs...)
			return
		}

		s.logger.Info("http request", attrs...)
	})
}

func (s *Handler) recordHTTPRequest(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		rec := &responseRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.metrics.RecordHTTPRequest(r.Method, route, status, time.Since(startedAt))
	})
}

func (s *Handler) limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > s.config.MaxRequestBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, s.config.MaxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	wroteHeader bool
	status      int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}

	if status >= 200 {
		r.wroteHeader = true
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(data)
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (r *responseRecorder) Flush() {
	if !r.wroteHeader {
		r.wroteHeader = true
		r.status = http.StatusOK
	}
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
