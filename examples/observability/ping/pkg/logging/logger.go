// Copyright (c) The Thanos Authors.
// Licensed under the Apache License 2.0.

// Copied from https://github.com/thanos-io/thanos/tree/19dcc7902d2431265154cefff82426fbc91448a3/pkg/logging

package logging

import (
	"io"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

const (
	LogFormatLogfmt = "logfmt"
	LogFormatJSON   = "json"
)

// NewLogger returns a log.Logger that prints in the provided format at the
// provided level with a UTC timestamp and the caller of the log entry. If non
// empty, the debug name is also appended as a field to all log lines. Panics
// if the log level is not error, warn, info or debug. Log level is expected to
// be validated before passed to this function.
func NewLogger(logLevel, logFormat, debugName string, w io.Writer) log.Logger {
	var (
		logger log.Logger
		lvl    level.Option
	)

	switch logLevel {
	case "error":
		lvl = level.AllowError()
	case "warn":
		lvl = level.AllowWarn()
	case "info":
		lvl = level.AllowInfo()
	case "debug":
		lvl = level.AllowDebug()
	default:
		// This enum is already checked and enforced by flag validations, so
		// this should never happen.
		panic("unexpected log level")
	}

	logger = log.NewLogfmtLogger(log.NewSyncWriter(w))
	if logFormat == LogFormatJSON {
		logger = log.NewJSONLogger(log.NewSyncWriter(w))
	}

	logger = level.NewFilter(logger, lvl)

	if debugName != "" {
		logger = log.With(logger, "name", debugName)
	}

	return log.With(logger, "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
}
