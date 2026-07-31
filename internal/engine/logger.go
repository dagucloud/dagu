// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
)

type slogAdapter struct {
	logger *slog.Logger
}

func newSlogAdapter(logger *slog.Logger) *slogAdapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &slogAdapter{logger: logger}
}

func (l *slogAdapter) Debug(msg string, tags ...slog.Attr) { l.log(slog.LevelDebug, msg, tags...) }
func (l *slogAdapter) Info(msg string, tags ...slog.Attr)  { l.log(slog.LevelInfo, msg, tags...) }
func (l *slogAdapter) Warn(msg string, tags ...slog.Attr)  { l.log(slog.LevelWarn, msg, tags...) }
func (l *slogAdapter) Error(msg string, tags ...slog.Attr) { l.log(slog.LevelError, msg, tags...) }

// Fatal logs without exiting so embedded engines cannot terminate the host process.
func (l *slogAdapter) Fatal(msg string, tags ...slog.Attr) { l.log(slog.LevelError, msg, tags...) }

func (l *slogAdapter) Debugf(format string, v ...any) { l.Debug(fmt.Sprintf(format, v...)) }
func (l *slogAdapter) Infof(format string, v ...any)  { l.Info(fmt.Sprintf(format, v...)) }
func (l *slogAdapter) Warnf(format string, v ...any)  { l.Warn(fmt.Sprintf(format, v...)) }
func (l *slogAdapter) Errorf(format string, v ...any) { l.Error(fmt.Sprintf(format, v...)) }

// Fatalf logs without exiting so embedded engines cannot terminate the host process.
func (l *slogAdapter) Fatalf(format string, v ...any) { l.Fatal(fmt.Sprintf(format, v...)) }

func (l *slogAdapter) Write(msg string) {
	l.Info(msg)
}

func (l *slogAdapter) With(attrs ...slog.Attr) logger.Logger {
	args := make([]any, len(attrs))
	for i, attr := range attrs {
		args[i] = attr
	}
	return &slogAdapter{logger: l.logger.With(args...)}
}

func (l *slogAdapter) WithGroup(name string) logger.Logger {
	return &slogAdapter{logger: l.logger.WithGroup(name)}
}

func (l *slogAdapter) log(level slog.Level, msg string, tags ...slog.Attr) {
	args := make([]any, len(tags))
	for i, attr := range tags {
		args[i] = attr
	}
	l.logger.Log(context.Background(), level, msg, args...)
}
