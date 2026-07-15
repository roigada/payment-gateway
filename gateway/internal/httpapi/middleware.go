package httpapi

import (
	"net/http"
	"runtime/debug"
	"time"
)

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
		if r.ContentLength > s.options.MaxRequestBodyBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, s.options.MaxRequestBodyBytes)
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
