// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	slogmulti "github.com/samber/slog-multi"
)

type Logger interface {
	Debug(msg string, tags ...slog.Attr)
	Info(msg string, tags ...slog.Attr)
	Warn(msg string, tags ...slog.Attr)
	Error(msg string, tags ...slog.Attr)
	Fatal(msg string, tags ...slog.Attr)

	Debugf(format string, v ...any)
	Infof(format string, v ...any)
	Warnf(format string, v ...any)
	Errorf(format string, v ...any)
	Fatalf(format string, v ...any)

	With(attrs ...slog.Attr) Logger
	WithGroup(name string) Logger

	// Write writes a message to the logger in free form.
	Write(string)
}

var _ Logger = (*appLogger)(nil)

type appLogger struct {
	logger         *slog.Logger
	guardedHandler *guardedHandler
	quiet          bool
	debug          bool
}

type Config struct {
	debug     bool
	format    string
	writer    io.Writer
	quiet     bool
	runWriter bool
}

type Option func(*Config)

// WithDebug sets the level of the logger to debug.
func WithDebug() Option {
	return func(o *Config) {
		o.debug = true
	}
}

// WithFormat sets the format of the logger (text or json).
func WithFormat(format string) Option {
	return func(o *Config) {
		o.format = format
	}
}

// WithWriter sets the file to write logs to.
func WithWriter(w io.Writer) Option {
	return func(o *Config) {
		o.writer = w
	}
}

// WithRunWriter writes logs without context already identified by the run stream.
func WithRunWriter(w io.Writer) Option {
	return func(o *Config) {
		o.writer = w
		o.runWriter = true
	}
}

// WithQuiet suppresses output to stderr.
func WithQuiet() Option {
	return func(o *Config) {
		o.quiet = true
	}
}

// WithValues adds attributes to the context for structured logging
func WithValues(ctx context.Context, attrs ...slog.Attr) context.Context {
	// Create a new logger with these attributes
	logger := FromContext(ctx).With(attrs...)

	// Store the new logger in the context
	return context.WithValue(ctx, loggerKey{}, logger)
}

var defaultLogger = NewLogger(WithFormat("text"))

func NewLogger(opts ...Option) Logger {
	cfg := &Config{}
	for _, opt := range opts {
		opt(cfg)
	}

	var level slog.Level
	if cfg.debug {
		level = slog.LevelDebug
	} else {
		level = slog.LevelInfo
	}

	handlerOpts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug,
	}

	var (
		handlers       []slog.Handler
		guardedHandler *guardedHandler
	)

	if !cfg.quiet {
		consoleHandler := newHandler(os.Stderr, cfg.format, handlerOpts)
		handlers = append(handlers, consoleHandler)
	}

	if cfg.writer != nil {
		handler := newHandler(cfg.writer, cfg.format, handlerOpts)
		if cfg.runWriter {
			handler = newRunWriterHandler(handler)
		}
		guardedHandler = newGuardedHandler(handler, cfg.writer)
		handlers = append(handlers, guardedHandler)
	}

	return &appLogger{
		logger:         slog.New(slogmulti.Fanout(handlers...)),
		guardedHandler: guardedHandler,
		quiet:          cfg.quiet,
		debug:          cfg.debug,
	}
}

func isRunContext(key string) bool {
	switch key {
	case "dag", "run-id", "attempt-id", "worker-id", "trace-id", "span-id", "trace-flags", "root", "parent":
		return true
	default:
		return false
	}
}

type runWriterHandler struct {
	slog.Handler
}

var _ slog.Handler = (*runWriterHandler)(nil)

func newRunWriterHandler(handler slog.Handler) slog.Handler {
	return &runWriterHandler{Handler: handler}
}

func (h *runWriterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	filtered := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if !isRunContext(attr.Key) {
			filtered = append(filtered, attr)
		}
	}
	return &runWriterHandler{Handler: h.Handler.WithAttrs(filtered)}
}

func (h *runWriterHandler) WithGroup(name string) slog.Handler {
	return &runWriterHandler{Handler: h.Handler.WithGroup(name)}
}

var _ slog.Handler = (*guardedHandler)(nil)

// guardedHandler is a slog.Handler that guards writes to a file with a mutex.
// This is to allow appLogger to write to the same file without interleaving
// log lines. It assumes:
// 1. that the underlying handler is thread-safe and.
// 2. the file is opened with the O_SYNC flag to ensure that writes are atomic.
type guardedHandler struct {
	handler slog.Handler
	writer  io.Writer
	mu      sync.Mutex
}

func newGuardedHandler(handler slog.Handler, writer io.Writer) *guardedHandler {
	return &guardedHandler{
		handler: handler,
		writer:  writer,
	}
}

// Enabled implements slog.Handler.
func (s *guardedHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return s.handler.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (s *guardedHandler) Handle(ctx context.Context, record slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handler.Handle(ctx, record)
}

// WithAttrs implements slog.Handler.
func (s *guardedHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &guardedHandler{
		handler: s.handler.WithAttrs(attrs),
		writer:  s.writer,
		mu:      sync.Mutex{},
	}
}

// WithGroup implements slog.Handler.
func (s *guardedHandler) WithGroup(name string) slog.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &guardedHandler{
		handler: s.handler.WithGroup(name),
		writer:  s.writer,
		mu:      sync.Mutex{},
	}
}

func newHandler(w io.Writer, format string, opts *slog.HandlerOptions) slog.Handler {
	if format == "text" {
		return slog.NewTextHandler(w, opts)
	}
	return slog.NewJSONHandler(w, opts)
}

// Debugf implements logger.Logger.
func (a *appLogger) Debugf(format string, v ...any) {
	if a.debug {
		a.logWithPC(slog.LevelDebug, fmt.Sprintf(format, v...))
	} else {
		a.logger.Debug(fmt.Sprintf(format, v...))
	}
}

// Errorf implements logger.Logger.
func (a *appLogger) Errorf(format string, v ...any) {
	if a.debug {
		a.logWithPC(slog.LevelError, fmt.Sprintf(format, v...))
	} else {
		a.logger.Error(fmt.Sprintf(format, v...))
	}
}

// Fatalf implements logger.Logger.
func (a *appLogger) Fatalf(format string, v ...any) {
	if a.debug {
		a.logWithPC(slog.LevelError, fmt.Sprintf(format, v...))
	} else {
		a.logger.Error(fmt.Sprintf(format, v...))
	}
	os.Exit(1)
}

// Infof implements logger.Logger.
func (a *appLogger) Infof(format string, v ...any) {
	if a.debug {
		a.logWithPC(slog.LevelInfo, fmt.Sprintf(format, v...))
	} else {
		a.logger.Info(fmt.Sprintf(format, v...))
	}
}

// Warnf implements logger.Logger.
func (a *appLogger) Warnf(format string, v ...any) {
	if a.debug {
		a.logWithPC(slog.LevelWarn, fmt.Sprintf(format, v...))
	} else {
		a.logger.Warn(fmt.Sprintf(format, v...))
	}
}

// Debug implements logger.Logger.
func (a *appLogger) Debug(msg string, tags ...slog.Attr) {
	if a.debug {
		a.logWithPC(slog.LevelDebug, msg, tags...)
	} else {
		a.logger.LogAttrs(context.Background(), slog.LevelDebug, msg, tags...)
	}
}

// Error implements logger.Logger.
func (a *appLogger) Error(msg string, tags ...slog.Attr) {
	if a.debug {
		a.logWithPC(slog.LevelError, msg, tags...)
	} else {
		a.logger.LogAttrs(context.Background(), slog.LevelError, msg, tags...)
	}
}

// Fatal implements logger.Logger.
func (a *appLogger) Fatal(msg string, tags ...slog.Attr) {
	if a.debug {
		a.logWithPC(slog.LevelError, msg, tags...)
	} else {
		a.logger.LogAttrs(context.Background(), slog.LevelError, msg, tags...)
	}
	os.Exit(1)
}

// Info implements logger.Logger.
func (a *appLogger) Info(msg string, tags ...slog.Attr) {
	if a.debug {
		a.logWithPC(slog.LevelInfo, msg, tags...)
	} else {
		a.logger.LogAttrs(context.Background(), slog.LevelInfo, msg, tags...)
	}
}

// Warn implements logger.Logger.
func (a *appLogger) Warn(msg string, tags ...slog.Attr) {
	if a.debug {
		a.logWithPC(slog.LevelWarn, msg, tags...)
	} else {
		a.logger.LogAttrs(context.Background(), slog.LevelWarn, msg, tags...)
	}
}

// logWithPC logs with the correct program counter
func (a *appLogger) logWithPC(level slog.Level, msg string, tags ...slog.Attr) {
	if !a.logger.Enabled(context.Background(), level) {
		return
	}

	// Get the caller's PC
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:]) // Skip runtime.Callers, logWithPC, and the logger method

	record := slog.NewRecord(time.Now(), level, msg, pcs[0])
	record.AddAttrs(tags...)
	_ = a.logger.Handler().Handle(context.Background(), record)
}

// With implements logger.Logger.
func (a *appLogger) With(attrs ...slog.Attr) Logger {
	// Convert []slog.Attr to []any for slog.Logger.With
	anyAttrs := make([]any, len(attrs))
	for i, attr := range attrs {
		anyAttrs[i] = attr
	}
	return &appLogger{
		logger:         a.logger.With(anyAttrs...),
		guardedHandler: a.guardedHandler,
		quiet:          a.quiet,
		debug:          a.debug,
	}
}

// WithGroup implements logger.Logger.
func (a *appLogger) WithGroup(name string) Logger {
	return &appLogger{
		logger:         a.logger.WithGroup(name),
		guardedHandler: a.guardedHandler,
		quiet:          a.quiet,
		debug:          a.debug,
	}
}

func (a *appLogger) Write(msg string) {
	// write to the standard output
	if !a.quiet {
		_, _ = fmt.Fprintf(os.Stdout, "%s\n", msg)
	}
	if a.guardedHandler != nil {
		// If a guarded handler is present, write to the file
		a.guardedHandler.mu.Lock()
		defer a.guardedHandler.mu.Unlock()
		_, _ = a.guardedHandler.writer.Write([]byte(msg + "\n"))
	}
}
