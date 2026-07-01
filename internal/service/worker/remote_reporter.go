// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/worker/coordreport"
)

const (
	remoteSchedulerLogFinalizeTimeout = 5 * time.Second
	remoteTerminalStatusReportTimeout = 5 * time.Second
)

type remoteRunMetadata struct {
	dagRunID  string
	dagName   string
	attemptID string
	root      exec.DAGRunRef
}

func (m remoteRunMetadata) key() string {
	return m.dagRunID
}

func (m remoteRunMetadata) normalize() remoteRunMetadata {
	if m.root.Zero() && m.dagName != "" && m.dagRunID != "" {
		m.root = exec.NewDAGRunRef(m.dagName, m.dagRunID)
	}
	return m
}

func (m remoteRunMetadata) withRun(dagRunID, dagName, attemptID string, root exec.DAGRunRef) remoteRunMetadata {
	if dagRunID != "" {
		m.dagRunID = dagRunID
	}
	if dagName != "" {
		m.dagName = dagName
	}
	if attemptID != "" {
		m.attemptID = attemptID
	}
	if !root.Zero() {
		m.root = root
	}
	return m.normalize()
}

func (m remoteRunMetadata) withAttemptID(attemptID string) remoteRunMetadata {
	if attemptID != "" {
		m.attemptID = attemptID
	}
	return m
}

type remoteRunReporter struct {
	client    coordinator.Client
	workerID  string
	owner     exec.HostInfo
	mu        sync.Mutex
	defaults  remoteRunMetadata
	logs      map[string]*coordreport.LogStreamer
	artifacts map[string]*coordreport.ArtifactUploader
	finalizer *schedulerLogFinalizer
}

func newRemoteRunReporter(
	client coordinator.Client,
	workerID string,
	defaults remoteRunMetadata,
	owner exec.HostInfo,
) *remoteRunReporter {
	return &remoteRunReporter{
		client:    client,
		workerID:  workerID,
		owner:     owner,
		defaults:  defaults.normalize(),
		logs:      make(map[string]*coordreport.LogStreamer),
		artifacts: make(map[string]*coordreport.ArtifactUploader),
	}
}

func (r *remoteRunReporter) SetAttemptID(attemptID string) {
	if r == nil || attemptID == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.defaults.attemptID != "" {
		return
	}
	r.defaults.attemptID = attemptID

	key := r.defaults.key()
	if streamer := r.logs[key]; streamer != nil {
		streamer.SetAttemptID(attemptID)
	}
	if uploader := r.artifacts[key]; uploader != nil {
		uploader.SetAttemptID(attemptID)
	}
}

func (r *remoteRunReporter) EnableSchedulerFinalizer(logFile string) *schedulerLogFinalizer {
	if r == nil || logFile == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.finalizer == nil {
		r.finalizer = newSchedulerLogFinalizer()
	}
	r.finalizer.register(r.defaults, logFile)
	return r.finalizer
}

func (r *remoteRunReporter) NewStepWriter(ctx context.Context, stepName string, streamType int) io.WriteCloser {
	meta := r.metadataFromContext(ctx)
	streamer := r.logStreamerFor(meta)
	if streamer == nil {
		return &discardWriteCloser{}
	}
	return streamer.NewStepWriter(ctx, stepName, streamType)
}

func (r *remoteRunReporter) NewSchedulerLogWriter(ctx context.Context, localFile *os.File) io.WriteCloser {
	if r == nil {
		return nonClosingWriteCloser{Writer: localFile}
	}
	meta := r.metadataFromContext(ctx)
	streamer := r.logStreamerFor(meta)
	if streamer == nil {
		return nonClosingWriteCloser{Writer: localFile}
	}
	writer := streamer.NewSchedulerLogWriter(context.WithoutCancel(ctx), localFile)
	if finalizer := r.schedulerFinalizer(); finalizer != nil {
		finalizer.trackWriter(meta, localFileName(localFile), writer)
	}
	return writer
}

func (r *remoteRunReporter) StreamSchedulerLog(ctx context.Context, logFilePath string) error {
	if r == nil {
		return nil
	}

	meta := r.metadataFromContext(ctx)
	if entry := r.schedulerLogEntry(meta, logFilePath); entry != nil {
		_, err := entry.finalizeLog(ctx)
		return err
	}

	streamer := r.logStreamerFor(meta)
	if streamer == nil {
		return nil
	}
	return streamer.StreamSchedulerLog(ctx, logFilePath)
}

func (r *remoteRunReporter) Finalize(ctx context.Context, attemptID, dir string) error {
	uploader := r.artifactUploaderFor(r.metadataFromContext(ctx).withAttemptID(attemptID))
	if uploader == nil {
		return nil
	}
	return uploader.Finalize(ctx, attemptID, dir)
}

func (r *remoteRunReporter) finalizeSchedulerLogForStatus(ctx context.Context, status exec.DAGRunStatus) (bool, error) {
	if r == nil {
		return false, nil
	}

	meta := r.metadataFromStatus(status)
	entry := r.schedulerLogEntry(meta, status.Log)
	if entry == nil {
		return false, nil
	}
	return entry.finalizeLog(ctx)
}

func (r *remoteRunReporter) schedulerFinalizer() *schedulerLogFinalizer {
	r.mu.Lock()
	finalizer := r.finalizer
	r.mu.Unlock()
	return finalizer
}

func (r *remoteRunReporter) schedulerLogEntry(meta remoteRunMetadata, logFile string) *schedulerLogFinalizerEntry {
	finalizer := r.schedulerFinalizer()
	if finalizer == nil {
		return nil
	}
	meta = meta.normalize()
	entry := finalizer.entryForDAGRun(meta.dagRunID)
	if entry == nil && logFile != "" {
		entry = finalizer.entryForLogFile(logFile)
	}
	if entry == nil {
		return finalizer.register(meta, logFile)
	}
	return entry
}

func (r *remoteRunReporter) metadataFromContext(ctx context.Context) remoteRunMetadata {
	meta := r.defaultMetadata()
	if rCtx, ok := exec.LookupContext(ctx); ok {
		dagName := ""
		if rCtx.DAG != nil {
			dagName = rCtx.DAG.Name
		}
		return meta.withRun(rCtx.DAGRunID, dagName, rCtx.AttemptID, rCtx.RootDAGRun)
	}
	return meta.normalize()
}

func (r *remoteRunReporter) metadataFromStatus(status exec.DAGRunStatus) remoteRunMetadata {
	return r.defaultMetadata().withRun(status.DAGRunID, status.Name, status.AttemptID, status.Root)
}

func (r *remoteRunReporter) defaultMetadata() remoteRunMetadata {
	if r == nil {
		return remoteRunMetadata{}
	}
	r.mu.Lock()
	meta := r.defaults
	r.mu.Unlock()
	return meta.normalize()
}

func (r *remoteRunReporter) logStreamerFor(meta remoteRunMetadata) *coordreport.LogStreamer {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.logStreamerLocked(meta)
}

func (r *remoteRunReporter) logStreamerLocked(meta remoteRunMetadata) *coordreport.LogStreamer {
	meta = meta.normalize()
	key := meta.key()
	if key == "" {
		return nil
	}
	streamer := r.logs[key]
	if streamer == nil {
		streamer = coordreport.NewLogStreamer(
			r.client,
			r.workerID,
			meta.dagRunID,
			meta.dagName,
			meta.attemptID,
			meta.root,
			r.owner,
		)
		r.logs[key] = streamer
		return streamer
	}
	if meta.attemptID != "" {
		streamer.SetAttemptID(meta.attemptID)
	}
	return streamer
}

func (r *remoteRunReporter) artifactUploaderFor(meta remoteRunMetadata) *coordreport.ArtifactUploader {
	if r == nil {
		return nil
	}

	meta = meta.normalize()
	key := meta.key()
	if key == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	uploader := r.artifacts[key]
	if uploader == nil {
		uploader = coordreport.NewArtifactUploader(
			r.client,
			r.workerID,
			meta.dagRunID,
			meta.dagName,
			meta.attemptID,
			meta.root,
			r.owner,
		)
		r.artifacts[key] = uploader
		return uploader
	}
	if meta.attemptID != "" {
		uploader.SetAttemptID(meta.attemptID)
	}
	return uploader
}

type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) {
	return len(p), nil
}

func (discardWriteCloser) Close() error {
	return nil
}

type nonClosingWriteCloser struct {
	io.Writer
}

func (w nonClosingWriteCloser) Write(p []byte) (int, error) {
	if w.Writer == nil {
		return len(p), nil
	}
	return w.Writer.Write(p)
}

func (nonClosingWriteCloser) Close() error {
	return nil
}

type schedulerLogFinalizer struct {
	timeout   time.Duration
	mu        sync.Mutex
	byRunID   map[string]*schedulerLogFinalizerEntry
	byLogFile map[string]*schedulerLogFinalizerEntry
}

func newSchedulerLogFinalizer() *schedulerLogFinalizer {
	return &schedulerLogFinalizer{
		timeout:   remoteSchedulerLogFinalizeTimeout,
		byRunID:   make(map[string]*schedulerLogFinalizerEntry),
		byLogFile: make(map[string]*schedulerLogFinalizerEntry),
	}
}

func (f *schedulerLogFinalizer) register(meta remoteRunMetadata, logFile string) *schedulerLogFinalizerEntry {
	if f == nil || logFile == "" {
		return nil
	}
	meta = meta.normalize()
	if meta.dagRunID == "" {
		meta.dagRunID = dagRunIDFromSchedulerLogFile(logFile)
	}
	if meta.dagRunID == "" {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	bind := func(entry *schedulerLogFinalizerEntry) {
		if entry.logFile == "" {
			entry.logFile = logFile
		}
		f.byRunID[meta.dagRunID] = entry
		f.byLogFile[logFile] = entry
	}

	if entry := f.byRunID[meta.dagRunID]; entry != nil {
		bind(entry)
		return entry
	}
	if entry := f.byLogFile[logFile]; entry != nil {
		bind(entry)
		return entry
	}

	entry := &schedulerLogFinalizerEntry{timeout: f.timeout}
	bind(entry)
	return entry
}

func (f *schedulerLogFinalizer) trackWriter(meta remoteRunMetadata, logFile string, writer io.Closer) {
	entry := f.register(meta, logFile)
	if entry == nil {
		return
	}
	entry.trackWriter(writer)
}

func (f *schedulerLogFinalizer) entryForDAGRun(dagRunID string) *schedulerLogFinalizerEntry {
	if f == nil || dagRunID == "" {
		return nil
	}
	f.mu.Lock()
	entry := f.byRunID[dagRunID]
	f.mu.Unlock()
	return entry
}

func (f *schedulerLogFinalizer) entryForLogFile(logFile string) *schedulerLogFinalizerEntry {
	if f == nil || logFile == "" {
		return nil
	}
	f.mu.Lock()
	entry := f.byLogFile[logFile]
	f.mu.Unlock()
	return entry
}

func dagRunIDFromSchedulerLogFile(logFile string) string {
	base := filepath.Base(logFile)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return strings.TrimSuffix(base, ".log")
}

type schedulerLogFinalizerEntry struct {
	logFile     string
	timeout     time.Duration
	finalize    sync.Once
	finalizeErr error
	writerMu    sync.Mutex
	writer      io.Closer
}

func (e *schedulerLogFinalizerEntry) finalizeLog(ctx context.Context) (bool, error) {
	if e == nil {
		return false, nil
	}

	e.writerMu.Lock()
	writer := e.writer
	e.writerMu.Unlock()
	if writer == nil {
		return false, nil
	}

	var ran bool
	e.finalize.Do(func() {
		ran = true
		closeCtx, cancel := schedulerLogCloseContext(ctx, e.timeout)
		defer cancel()
		e.finalizeErr = closeSchedulerLogWriter(closeCtx, writer)
	})

	if !ran {
		return false, e.finalizeErr
	}
	return true, e.finalizeErr
}

func (e *schedulerLogFinalizerEntry) trackWriter(writer io.Closer) {
	if e == nil || writer == nil {
		return
	}

	e.writerMu.Lock()
	e.writer = writer
	e.writerMu.Unlock()
}

type schedulerLogContextCloser interface {
	CloseWithContext(context.Context) error
}

func schedulerLogCloseContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	closeCtx := context.WithoutCancel(ctx)
	if timeout <= 0 {
		return closeCtx, func() {}
	}
	return context.WithTimeout(closeCtx, timeout)
}

func closeSchedulerLogWriter(ctx context.Context, writer io.Closer) error {
	if writer == nil {
		return nil
	}
	if closer, ok := writer.(schedulerLogContextCloser); ok {
		return closer.CloseWithContext(ctx)
	}
	return writer.Close()
}

func localFileName(localFile *os.File) string {
	if localFile == nil {
		return ""
	}
	return localFile.Name()
}

type finalSchedulerLogStatusPusher struct {
	pusher    runtime.StatusPusher
	finalizer schedulerLogStatusFinalizer
}

type schedulerLogStatusFinalizer interface {
	finalizeSchedulerLogForStatus(context.Context, exec.DAGRunStatus) (bool, error)
}

func (p *finalSchedulerLogStatusPusher) Push(ctx context.Context, status exec.DAGRunStatus) error {
	if p.finalizer != nil && shouldFinalizeSchedulerLogBeforeStatus(status.Status) {
		if ran, err := p.finalizer.finalizeSchedulerLogForStatus(ctx, status); ran {
			if err != nil {
				logger.Warn(ctx, "Failed to finalize scheduler log before reporting terminal status",
					tag.RunID(status.DAGRunID),
					tag.Error(err))
			}
			pushCtx := context.WithoutCancel(ctx)
			if remoteTerminalStatusReportTimeout > 0 {
				var cancel context.CancelFunc
				pushCtx, cancel = context.WithTimeout(pushCtx, remoteTerminalStatusReportTimeout)
				defer cancel()
			}
			return p.pusher.Push(pushCtx, status)
		}
	}
	return p.pusher.Push(ctx, status)
}

func shouldFinalizeSchedulerLogBeforeStatus(status core.Status) bool {
	switch status {
	case core.Failed, core.Aborted, core.Succeeded, core.PartiallySucceeded, core.Rejected:
		return true
	case core.NotStarted, core.Running, core.Queued, core.Waiting:
		return false
	}
	return false
}
