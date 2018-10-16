package utils

import (
	"context"

	logrus "github.com/sirupsen/logrus"
)

// Inspired by ContainerD code https://github.com/containerd/containerd/blob/master/log/context.go

type (
	loggerKey struct{}
)

var defaultLogger = logrus.NewEntry(logrus.StandardLogger())

// WithLogger returns a new context with the provided logger. Use in
// combination with logger.WithField(s) for great effect.
func WithLogger(ctx context.Context, logger *logrus.Entry) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// GetLogger retrieves the current logger from the context. If no logger is
// available, the default logger is returned.
func GetLogger(ctx context.Context) *logrus.Entry {
	logger := ctx.Value(loggerKey{})

	if logger == nil {
		defaultLogger.Info("GetLogger called on Context without logger")
		return defaultLogger
	}

	return logger.(*logrus.Entry)
}
