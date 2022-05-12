// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

// Copied from https://github.com/thanos-io/thanos/tree/19dcc7902d2431265154cefff82426fbc91448a3/pkg/logging

package logging

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// ResponseWriterWithStatus wraps around http.ResponseWriter to capture the status code of the response.
type ResponseWriterWithStatus struct {
	http.ResponseWriter
	statusCode      int
	isHeaderWritten bool
}

// WrapResponseWriterWithStatus wraps the http.ResponseWriter for extracting status.
func WrapResponseWriterWithStatus(w http.ResponseWriter) *ResponseWriterWithStatus {
	return &ResponseWriterWithStatus{ResponseWriter: w}
}

// Status returns http response status.
func (r *ResponseWriterWithStatus) Status() string {
	return fmt.Sprintf("%v", r.statusCode)
}

// StatusCode returns http response status code.
func (r *ResponseWriterWithStatus) StatusCode() int {
	return r.statusCode
}

// WriteHeader writes the header.
func (r *ResponseWriterWithStatus) WriteHeader(code int) {
	if !r.isHeaderWritten {
		r.statusCode = code
		r.ResponseWriter.WriteHeader(code)
		r.isHeaderWritten = true
	}
}

type HTTPServerMiddleware struct {
	opts   *options
	logger log.Logger
}

func (m *HTTPServerMiddleware) preCall(name string, start time.Time, r *http.Request) {
	logger := m.opts.filterLog(m.logger)
	level.Debug(logger).Log("http.start_time", start.String(), "http.method", fmt.Sprintf("%s %s", r.Method, r.URL), "http.request_id", r.Header.Get("X-Request-ID"), "thanos.method_name", name, "msg", "started call")
}

func (m *HTTPServerMiddleware) postCall(name string, start time.Time, wrapped *ResponseWriterWithStatus, r *http.Request) {
	logger := log.With(m.logger, "http.method", fmt.Sprintf("%s %s", r.Method, r.URL), "http.request_id", r.Header.Get("X-Request-ID"), "http.status_code", wrapped.Status(),
		"http.time_ms", fmt.Sprintf("%v", durationToMilliseconds(time.Since(start))), "http.remote_addr", r.RemoteAddr, "thanos.method_name", name)

	logger = m.opts.filterLog(logger)
	m.opts.levelFunc(logger, wrapped.StatusCode()).Log("msg", "finished call")
}

func (m *HTTPServerMiddleware) HTTPMiddleware(name string, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		wrapped := WrapResponseWriterWithStatus(w)
		start := time.Now()
		hostPort := r.Host
		if hostPort == "" {
			hostPort = r.URL.Host
		}

		var port string
		var err error
		// Try to extract port if there is ':' as part of 'hostPort'.
		if strings.Contains(hostPort, ":") {
			_, port, err = net.SplitHostPort(hostPort)
			if err != nil {
				level.Error(m.logger).Log("msg", "failed to parse host port for http log decision", "err", err)
				next.ServeHTTP(w, r)
				return
			}
		}

		deciderURL := r.URL.String()
		if len(port) > 0 {
			deciderURL = net.JoinHostPort(deciderURL, port)
		}
		decision := m.opts.shouldLog(deciderURL, nil)

		switch decision {
		case NoLogCall:
			next.ServeHTTP(w, r)

		case LogStartAndFinishCall:
			m.preCall(name, start, r)
			next.ServeHTTP(wrapped, r)
			m.postCall(name, start, wrapped, r)

		case LogFinishCall:
			next.ServeHTTP(wrapped, r)
			m.postCall(name, start, wrapped, r)
		}
	}
}

// NewHTTPServerMiddleware returns an http middleware.
func NewHTTPServerMiddleware(logger log.Logger, opts ...Option) *HTTPServerMiddleware {
	o := evaluateOpt(opts)
	return &HTTPServerMiddleware{
		logger: log.With(logger, "protocol", "http", "http.component", "server"),
		opts:   o,
	}
}

// getHTTPLoggingOption returns the logging ENUM based on logStart and logEnd values.
func getHTTPLoggingOption(logStart, logEnd bool) (Decision, error) {
	if !logStart && !logEnd {
		return NoLogCall, nil
	}
	if !logStart && logEnd {
		return LogFinishCall, nil
	}
	if logStart && logEnd {
		return LogStartAndFinishCall, nil
	}
	return -1, fmt.Errorf("log start call is not supported")
}

// getLevel returns the level based logger.
func getLevel(lvl string) level.Option {
	switch lvl {
	case "INFO":
		return level.AllowInfo()
	case "DEBUG":
		return level.AllowDebug()
	case "WARN":
		return level.AllowWarn()
	case "ERROR":
		return level.AllowError()
	default:
		return level.AllowAll()
	}
}
