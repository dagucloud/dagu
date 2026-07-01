// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/logpath"
	"github.com/dagucloud/dagu/internal/cmn/secrets"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/node"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/proto/convert"
	"github.com/dagucloud/dagu/internal/runtime"
	rtagent "github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/worker/coordreport"
	dagutools "github.com/dagucloud/dagu/internal/tools"
	daguaqua "github.com/dagucloud/dagu/internal/tools/aqua"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

var _ TaskHandler = (*remoteTaskHandler)(nil)

// RemoteTaskHandlerConfig contains configuration for the remote task handler
type RemoteTaskHandlerConfig struct {
	// WorkerID is the identifier of this worker
	WorkerID string
	// CoordinatorClient is the coordinator client with load balancing support
	CoordinatorClient coordinator.Client
	// DAGStore is the store for DAG definitions
	DAGStore exec.DAGStore
	// DAGRunMgr is the manager for DAG runs
	DAGRunMgr runtime.Manager
	// StateStore is the persistent state store shared across DAG runs.
	StateStore dagstate.Store
	// ServiceRegistry is the service registry
	ServiceRegistry exec.ServiceRegistry
	// PeerConfig is the peer configuration
	PeerConfig config.Peer
	// Config is the main application configuration
	Config *config.Config
	// SecretStore resolves Dagu-managed secrets during execution.
	SecretStore secret.Store
	// ProfileStore resolves profile values during execution.
	ProfileStore profile.Store
}

type runtimeStores struct {
	SecretStore  secret.Store
	ProfileStore profile.Store
}

// NewRemoteTaskHandler creates a new TaskHandler that runs tasks in-process
// with status pushing and log streaming to the coordinator.
func NewRemoteTaskHandler(cfg RemoteTaskHandlerConfig) TaskHandler {
	if cfg.Config == nil {
		cfg.Config = &config.Config{}
	}
	stateStore := cfg.StateStore
	if stateStore == nil {
		if stateClient, ok := cfg.CoordinatorClient.(coordinator.StateClient); ok {
			stateStore = coordinator.NewStateStoreClient(stateClient)
		}
	}
	return &remoteTaskHandler{
		workerID:          cfg.WorkerID,
		coordinatorClient: cfg.CoordinatorClient,
		dagStore:          cfg.DAGStore,
		dagRunMgr:         cfg.DAGRunMgr,
		stateStore:        stateStore,
		serviceRegistry:   cfg.ServiceRegistry,
		peerConfig:        cfg.PeerConfig,
		config:            cfg.Config,
		runtimeStores: runtimeStores{
			SecretStore:  cfg.SecretStore,
			ProfileStore: cfg.ProfileStore,
		},
	}
}

type remoteTaskHandler struct {
	workerID          string
	coordinatorClient coordinator.Client
	dagStore          exec.DAGStore
	dagRunMgr         runtime.Manager
	stateStore        dagstate.Store
	serviceRegistry   exec.ServiceRegistry
	peerConfig        config.Peer
	config            *config.Config
	runtimeStores     runtimeStores
}

const (
	remoteSchedulerLogReplayTimeout   = 5 * time.Second
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
		r.finalizer.timeout = remoteSchedulerLogReplayTimeout
	}
	streamer := r.logStreamerLocked(r.defaults)
	r.finalizer.register(r.defaults, logFile, streamer)
	return r.finalizer
}

func (r *remoteRunReporter) schedulerFinalizer() *schedulerLogFinalizer {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finalizer
}

func (r *remoteRunReporter) NewStepWriter(ctx context.Context, stepName string, streamType int) io.WriteCloser {
	meta := r.metadataFromContext(ctx)
	streamer := r.logStreamerFor(meta)
	if streamer == nil {
		return &discardWriteCloser{}
	}
	return &schedulerMirroringStepWriter{
		WriteCloser: streamer.NewStepWriter(ctx, stepName, streamType),
		mirror: func(data []byte) {
			r.mirrorStepOutput(meta, data)
		},
	}
}

func (r *remoteRunReporter) NewSchedulerLogWriter(ctx context.Context, localFile *os.File) io.WriteCloser {
	writer := &schedulerLogFileWriter{localFile: localFile}
	if r.schedulerFinalizer() == nil {
		return writer
	}

	meta := r.metadataFromContext(ctx)
	entry, _ := r.schedulerLogEntry(meta, "", localFileName(localFile))
	if entry == nil {
		return writer
	}
	entry.trackWriter(writer)
	return writer
}

func (r *remoteRunReporter) StreamSchedulerLog(ctx context.Context, logFilePath string) error {
	if r == nil {
		return nil
	}

	entry, streamer := r.schedulerLogEntry(r.metadataFromContext(ctx), "", logFilePath)
	if entry == nil && streamer == nil {
		return nil
	}
	if entry == nil {
		return streamer.StreamSchedulerLog(ctx, logFilePath)
	}

	_, err := entry.finalizeLog(ctx)
	return err
}

func (r *remoteRunReporter) Finalize(ctx context.Context, attemptID, dir string) error {
	uploader := r.artifactUploaderFor(r.metadataFromContext(ctx).withAttemptID(attemptID))
	if uploader == nil {
		return nil
	}
	return uploader.Finalize(ctx, attemptID, dir)
}

func (r *remoteRunReporter) finalizeSchedulerLogForStatus(ctx context.Context, status exec.DAGRunStatus) (bool, error) {
	if r == nil || r.schedulerFinalizer() == nil {
		return false, nil
	}

	meta := r.metadataFromStatus(status)
	entry, _ := r.schedulerLogEntry(meta, status.DAGRunID, status.Log)
	if entry == nil {
		return false, nil
	}
	return entry.finalizeLog(ctx)
}

func (r *remoteRunReporter) mirrorStepOutput(meta remoteRunMetadata, data []byte) {
	if r == nil || len(data) == 0 {
		return
	}

	finalizer := r.schedulerFinalizer()
	if finalizer == nil {
		return
	}
	entry := finalizer.entryForDAGRun(meta.normalize().dagRunID)
	if entry == nil {
		return
	}
	entry.mirrorStepOutput(data)
}

func (r *remoteRunReporter) schedulerLogEntry(meta remoteRunMetadata, dagRunID, logFile string) (*schedulerLogFinalizerEntry, *coordreport.LogStreamer) {
	streamer := r.logStreamerFor(meta)
	if streamer == nil {
		return nil, nil
	}

	finalizer := r.schedulerFinalizer()
	if finalizer == nil {
		return nil, streamer
	}

	var entry *schedulerLogFinalizerEntry
	if dagRunID != "" {
		entry = finalizer.entryForDAGRun(dagRunID)
	}
	if entry == nil && logFile != "" {
		entry = finalizer.entryForLogFile(logFile)
	}
	if entry == nil && logFile != "" {
		entry = finalizer.register(meta, logFile, streamer)
	} else if entry != nil {
		entry.update(meta, streamer)
	}
	return entry, streamer
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

type schedulerMirroringStepWriter struct {
	io.WriteCloser
	mirror func([]byte)
}

func (w *schedulerMirroringStepWriter) Write(p []byte) (int, error) {
	n, err := w.WriteCloser.Write(p)
	if n > 0 && w.mirror != nil {
		w.mirror(p[:n])
	}
	return n, err
}

type schedulerLogFinalizer struct {
	timeout   time.Duration
	mu        sync.Mutex
	byRunID   map[string]*schedulerLogFinalizerEntry
	byLogFile map[string]*schedulerLogFinalizerEntry
}

func newSchedulerLogFinalizer() *schedulerLogFinalizer {
	return &schedulerLogFinalizer{
		byRunID:   make(map[string]*schedulerLogFinalizerEntry),
		byLogFile: make(map[string]*schedulerLogFinalizerEntry),
	}
}

func (f *schedulerLogFinalizer) register(meta remoteRunMetadata, logFile string, streamer runtime.SchedulerLogStreamer) *schedulerLogFinalizerEntry {
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

	if entry := f.byRunID[meta.dagRunID]; entry != nil {
		f.bindEntryLocked(entry, meta.dagRunID, logFile)
		entry.update(meta, streamer)
		return entry
	}
	if entry := f.byLogFile[logFile]; entry != nil {
		f.bindEntryLocked(entry, meta.dagRunID, logFile)
		entry.update(meta, streamer)
		return entry
	}

	entry := &schedulerLogFinalizerEntry{
		owner:    f,
		logFile:  logFile,
		streamer: streamer,
	}
	f.bindEntryLocked(entry, meta.dagRunID, logFile)
	return entry
}

func (f *schedulerLogFinalizer) bindEntryLocked(entry *schedulerLogFinalizerEntry, dagRunID, logFile string) {
	if entry == nil {
		return
	}
	if dagRunID != "" {
		f.byRunID[dagRunID] = entry
	}
	if logFile != "" {
		if entry.logFile == "" {
			entry.logFile = logFile
		}
		f.byLogFile[logFile] = entry
	}
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
	owner       *schedulerLogFinalizer
	logFile     string
	streamer    runtime.SchedulerLogStreamer
	finalize    sync.Once
	finalizeErr error
	writerMu    sync.Mutex
	writer      *schedulerLogFileWriter
}

func (e *schedulerLogFinalizerEntry) update(meta remoteRunMetadata, streamer runtime.SchedulerLogStreamer) {
	if e == nil {
		return
	}
	e.writerMu.Lock()
	defer e.writerMu.Unlock()
	e.updateLocked(meta, streamer)
}

func (e *schedulerLogFinalizerEntry) updateLocked(meta remoteRunMetadata, streamer runtime.SchedulerLogStreamer) {
	meta = meta.normalize()
	if streamer != nil {
		e.streamer = streamer
	}
	if meta.attemptID != "" {
		if setter, ok := e.streamer.(interface{ SetAttemptID(string) }); ok {
			setter.SetAttemptID(meta.attemptID)
		}
	}
}

func (e *schedulerLogFinalizerEntry) finalizeLog(ctx context.Context) (bool, error) {
	if e == nil {
		return false, nil
	}

	var ran bool
	e.finalize.Do(func() {
		ran = true
		e.quiesceWriter()

		streamCtx := context.WithoutCancel(ctx)
		if e.owner.timeout > 0 {
			var cancel context.CancelFunc
			streamCtx, cancel = context.WithTimeout(streamCtx, e.owner.timeout)
			defer cancel()
		}

		e.writerMu.Lock()
		streamer := e.streamer
		e.writerMu.Unlock()
		if streamer == nil {
			return
		}
		e.finalizeErr = streamer.StreamSchedulerLog(streamCtx, e.logFile)
	})

	if !ran {
		return false, e.finalizeErr
	}
	return true, e.finalizeErr
}

func (e *schedulerLogFinalizerEntry) trackWriter(writer *schedulerLogFileWriter) {
	if e == nil || writer == nil {
		return
	}

	e.writerMu.Lock()
	e.writer = writer
	e.writerMu.Unlock()
}

func (e *schedulerLogFinalizerEntry) quiesceWriter() {
	e.writerMu.Lock()
	writer := e.writer
	e.writerMu.Unlock()
	if writer == nil {
		return
	}
	writer.finalize()
}

func (e *schedulerLogFinalizerEntry) mirrorStepOutput(data []byte) {
	e.writerMu.Lock()
	writer := e.writer
	e.writerMu.Unlock()
	if writer == nil {
		return
	}
	_, _ = writer.Write(data)
}

type schedulerLogFileWriter struct {
	localFile *os.File
	mu        sync.Mutex
	finalized bool
}

func (w *schedulerLogFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.finalized || w.localFile == nil {
		return len(p), nil
	}
	return w.localFile.Write(p)
}

func (w *schedulerLogFileWriter) Close() error {
	return nil
}

func (w *schedulerLogFileWriter) finalize() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.finalized = true
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
	finalized := false
	if p.finalizer != nil && shouldFinalizeSchedulerLogBeforeStatus(status.Status) {
		if ran, err := p.finalizer.finalizeSchedulerLogForStatus(ctx, status); ran {
			finalized = true
			if err != nil {
				logger.Warn(ctx, "Failed to finalize scheduler log before reporting terminal status",
					tag.RunID(status.DAGRunID),
					tag.Error(err))
			}
		}
	}
	if finalized {
		pushCtx := context.WithoutCancel(ctx)
		if remoteTerminalStatusReportTimeout > 0 {
			var cancel context.CancelFunc
			pushCtx, cancel = context.WithTimeout(pushCtx, remoteTerminalStatusReportTimeout)
			defer cancel()
		}
		return p.pusher.Push(pushCtx, status)
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

func withFinalSchedulerLogStatusPusher(
	statusPusher runtime.StatusPusher,
	logStreamer runtime.SchedulerLogStreamer,
	logFile string,
) (runtime.StatusPusher, schedulerLogStatusFinalizer) {
	reporter, ok := logStreamer.(*remoteRunReporter)
	if !ok || reporter.EnableSchedulerFinalizer(logFile) == nil {
		return statusPusher, nil
	}
	if statusPusher == nil {
		return nil, reporter
	}
	return &finalSchedulerLogStatusPusher{
		pusher:    statusPusher,
		finalizer: reporter,
	}, reporter
}

// Handle executes a task in-process with remote status/log streaming
func (h *remoteTaskHandler) Handle(ctx context.Context, task *coordinatorv1.Task) error {
	logger.Info(ctx, "Executing remote task",
		slog.String("operation", task.Operation.String()),
		tag.Target(task.Target),
		tag.RunID(task.DagRunId),
		slog.String("root-dag-run-id", task.RootDagRunId),
		slog.String("parent-dag-run-id", task.ParentDagRunId))

	switch task.Operation {
	case coordinatorv1.Operation_OPERATION_START:
		return h.handleStart(ctx, task, false)

	case coordinatorv1.Operation_OPERATION_RETRY:
		return h.handleRetry(ctx, task)

	case coordinatorv1.Operation_OPERATION_UNSPECIFIED:
		return fmt.Errorf("unsupported operation: unspecified")

	default:
		return fmt.Errorf("unsupported operation: %v", task.Operation)
	}
}

func (h *remoteTaskHandler) handleStart(ctx context.Context, task *coordinatorv1.Task, queuedRun bool) error {
	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	parent := exec.DAGRunRef{Name: task.ParentDagRunName, ID: task.ParentDagRunId}
	owner, err := taskOwner(task)
	if err != nil {
		return fmt.Errorf("invalid task owner coordinator metadata: %w", err)
	}

	dag, cleanup, err := h.loadDAG(ctx, task)
	if err != nil {
		h.reportTaskLoadFailure(ctx, task, root, parent, owner, err, task.ProfileName)
		return fmt.Errorf("failed to load DAG: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	statusPusher, logStreamer, artifactUploader := h.createRemoteHandlers(task.DagRunId, dag.Name, task.AttemptId, root, owner)
	err = h.executeDAGRun(ctx, dag, task.DagRunId, task.AttemptId, task.AttemptKey, task.ScheduleTime, root, parent, owner, statusPusher, logStreamer, artifactUploader, queuedRun, nil, taskExtraEnvs(task), task.ProfileName)
	var initErr *taskInitError
	if errors.As(err, &initErr) && !initErr.reported {
		h.reportTaskInitFailure(ctx, task, root, parent, statusPusher, initErr.err, task.ProfileName)
	}
	return err
}

func (h *remoteTaskHandler) handleRetry(ctx context.Context, task *coordinatorv1.Task) error {
	root := exec.DAGRunRef{Name: task.RootDagRunName, ID: task.RootDagRunId}
	parent := exec.DAGRunRef{Name: task.ParentDagRunName, ID: task.ParentDagRunId}
	owner, err := taskOwner(task)
	if err != nil {
		return fmt.Errorf("invalid task owner coordinator metadata: %w", err)
	}

	if task.PreviousStatus == nil {
		return fmt.Errorf("retry requires previous_status in task")
	}

	status, convErr := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if convErr != nil {
		return fmt.Errorf("failed to convert previous status: %w", convErr)
	}
	profileName := retryTaskProfileName(status)
	logger.Info(ctx, "Using previous status from task for retry",
		tag.RunID(task.DagRunId),
		slog.Int("nodes", len(status.Nodes)))

	dag, cleanup, err := h.loadDAG(ctx, task)
	if err != nil {
		h.reportTaskLoadFailure(ctx, task, root, parent, owner, err, profileName)
		return fmt.Errorf("failed to load DAG: %w", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	statusPusher, logStreamer, artifactUploader := h.createRemoteHandlers(task.DagRunId, dag.Name, task.AttemptId, root, owner)
	triggerType := exec.PreservedQueueTriggerType(status)

	err = h.executeDAGRun(ctx, dag, task.DagRunId, task.AttemptId, task.AttemptKey, task.ScheduleTime, root, parent, owner, statusPusher, logStreamer, artifactUploader, false, &retryConfig{
		target:      status,
		stepName:    task.Step,
		triggerType: triggerType,
	}, taskExtraEnvs(task), profileName)
	var initErr *taskInitError
	if errors.As(err, &initErr) && !initErr.reported {
		h.reportTaskInitFailure(ctx, task, root, parent, statusPusher, initErr.err, profileName)
	}
	return err
}

func retryTaskProfileName(status *exec.DAGRunStatus) string {
	if status == nil {
		return ""
	}
	return status.ProfileName
}

func (h *remoteTaskHandler) reportTaskLoadFailure(ctx context.Context, task *coordinatorv1.Task, root, parent exec.DAGRunRef, owner exec.HostInfo, loadErr error, profileName string) {
	statusPusher := coordreport.NewStatusPusher(h.coordinatorClient, h.workerID, owner)
	finishedAt := stringutil.FormatTime(time.Now())
	logger.Warn(ctx, "Failed to load DAG on worker",
		tag.Target(task.Target),
		tag.RunID(task.DagRunId),
		tag.Error(loadErr),
	)
	status := exec.DAGRunStatus{
		Root:        root,
		Parent:      parent,
		Name:        task.Target,
		DAGRunID:    task.DagRunId,
		AttemptID:   task.AttemptId,
		Status:      core.Failed,
		FinishedAt:  finishedAt,
		Error:       sanitizeTaskLoadError(task.Target, loadErr),
		Params:      task.Params,
		ProfileName: profileName,
	}

	if err := statusPusher.Push(ctx, status); err != nil {
		logger.Warn(ctx, "Failed to report load failure status",
			tag.Target(task.Target),
			tag.RunID(task.DagRunId),
			tag.Error(err),
		)
	}
}

func (h *remoteTaskHandler) reportTaskInitFailure(
	ctx context.Context,
	task *coordinatorv1.Task,
	root exec.DAGRunRef,
	parent exec.DAGRunRef,
	statusPusher runtime.StatusPusher,
	initErr error,
	profileName string,
) {
	if statusPusher == nil || initErr == nil {
		return
	}

	h.reportDAGRunInitFailure(ctx, task.Target, task.DagRunId, task.AttemptId, task.Params, root, parent, statusPusher, initErr, profileName)
}

func (h *remoteTaskHandler) reportDAGRunInitFailure(
	ctx context.Context,
	target string,
	dagRunID string,
	attemptID string,
	params string,
	root exec.DAGRunRef,
	parent exec.DAGRunRef,
	statusPusher runtime.StatusPusher,
	initErr error,
	profileName string,
) {
	if statusPusher == nil || initErr == nil {
		return
	}

	finishedAt := stringutil.FormatTime(time.Now())
	logger.Warn(ctx, "Failed to initialize DAG on worker",
		tag.Target(target),
		tag.RunID(dagRunID),
		tag.Error(initErr),
	)
	status := exec.DAGRunStatus{
		Root:        root,
		Parent:      parent,
		Name:        target,
		DAGRunID:    dagRunID,
		AttemptID:   attemptID,
		Status:      core.Failed,
		FinishedAt:  finishedAt,
		Error:       initErr.Error(),
		Params:      params,
		ProfileName: profileName,
	}

	if err := statusPusher.Push(ctx, status); err != nil {
		logger.Warn(ctx, "Failed to report init failure status",
			tag.Target(target),
			tag.RunID(dagRunID),
			tag.Error(err),
		)
	}
}

func sanitizeTaskLoadError(target string, loadErr error) string {
	message := loadErr.Error()
	rest, ok := strings.CutPrefix(message, "failed to load DAG from ")
	if !ok {
		return message
	}

	if _, reason, ok := strings.Cut(rest, ": "); ok {
		return fmt.Sprintf("failed to load DAG %q: %s", target, reason)
	}

	return fmt.Sprintf("failed to load DAG %q", target)
}

// retryConfig holds retry-specific configuration
type retryConfig struct {
	target      *exec.DAGRunStatus
	stepName    string
	triggerType core.TriggerType
}

type taskInitError struct {
	err      error
	reported bool
}

func (e *taskInitError) Error() string {
	return e.err.Error()
}

func (e *taskInitError) Unwrap() error {
	return e.err
}

func newTaskInitError(err error) error {
	if err == nil {
		return nil
	}
	return &taskInitError{err: err}
}

func newReportedTaskInitError(err error) error {
	if err == nil {
		return nil
	}
	return &taskInitError{err: err, reported: true}
}

func taskExtraEnvs(task *coordinatorv1.Task) []string {
	if task == nil || !task.ExternalStepRetry {
		return nil
	}
	return []string{exec.EnvKeyExternalStepRetry + "=1"}
}

// createRemoteHandlers creates the remote status, log, and artifact transport handlers.
func (h *remoteTaskHandler) createRemoteHandlers(dagRunID, dagName, attemptID string, root exec.DAGRunRef, owner ...exec.HostInfo) (runtime.StatusPusher, runtime.SchedulerLogStreamer, runtime.ArtifactFinalizer) {
	var target exec.HostInfo
	if len(owner) > 0 {
		target = owner[0]
	}
	statusPusher := coordreport.NewStatusPusher(h.coordinatorClient, h.workerID, target)
	reporter := newRemoteRunReporter(
		h.coordinatorClient,
		h.workerID,
		remoteRunMetadata{
			dagRunID:  dagRunID,
			dagName:   dagName,
			attemptID: attemptID,
			root:      root,
		},
		target,
	)
	return statusPusher, reporter, reporter
}

// loadDAG loads the DAG from task definition.
// Returns the loaded DAG and a cleanup function that should be called after task execution.
func (h *remoteTaskHandler) loadDAG(ctx context.Context, task *coordinatorv1.Task) (*core.DAG, func(), error) {
	if _, ok, err := taskWorkspaceDescriptor(task); err != nil {
		return nil, nil, err
	} else if ok {
		return h.loadActionWorkspaceDAG(ctx, task)
	}

	logger.Info(ctx, "Creating temporary DAG file from definition",
		tag.DAG(task.Target),
		tag.Size(len(task.Definition)))

	tempFile, err := fileutil.CreateTempDAGFile("worker-dags", task.Target, []byte(task.Definition))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create temp DAG file: %w", err)
	}
	cleanupFunc := func() {
		if err := os.Remove(tempFile); err != nil && !os.IsNotExist(err) {
			logger.Errorf(ctx, "Failed to remove temp DAG file: %v", err)
		}
	}

	// Remote tasks load the DAG definition received from the coordinator.
	// Local DAG directories are outside the task payload boundary.
	loadOpts := []spec.LoadOption{
		spec.WithName(task.Target), // Use original DAG name, not temp file path
	}

	// Use embedded base config from the task if available (distributed mode).
	// Fall back to local base config path if the task doesn't include one.
	if task.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfigContent([]byte(task.BaseConfig)))
	} else {
		loadOpts = append(loadOpts, spec.WithBaseConfig(h.config.Paths.BaseConfig))
	}

	// Pass task params to the DAG (e.g., from parallel execution items)
	if task.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(task.Params))
	} else if params, err := previousStatusParams(task); err != nil {
		cleanupFunc()
		return nil, nil, err
	} else if len(params) > 0 {
		loadOpts = append(loadOpts, spec.WithParams(spec.QuoteRuntimeParams(params, nil)))
	}

	dag, err := spec.Load(ctx, tempFile, loadOpts...)
	if err != nil {
		cleanupFunc()
		return nil, nil, fmt.Errorf("failed to load DAG from %s: %w", tempFile, err)
	}
	dag.SourceFile = task.SourceFile

	return dag, cleanupFunc, nil
}

func (h *remoteTaskHandler) loadActionWorkspaceDAG(ctx context.Context, task *coordinatorv1.Task) (*core.DAG, func(), error) {
	client, ok := h.coordinatorClient.(workspacebundle.Client)
	if !ok {
		return nil, nil, fmt.Errorf("coordinator client does not support workspace bundles")
	}

	workDir := remoteActionWorkDir(task)
	workspace, err := materializeTaskWorkspace(ctx, task, client, actionWorkspaceDir(workDir))
	if err != nil {
		return nil, nil, err
	}
	cleanupFunc := func() {
		if err := os.RemoveAll(workDir); err != nil {
			logger.Warn(ctx, "Failed to remove action workspace",
				slog.String("path", workDir),
				tag.Error(err))
		}
	}

	loadOpts := []spec.LoadOption{
		spec.WithName(task.Target),
		spec.WithDefaultWorkingDir(workspace.dir),
	}
	if task.Params != "" {
		loadOpts = append(loadOpts, spec.WithParams(task.Params))
	} else if params, err := previousStatusParams(task); err != nil {
		cleanupFunc()
		return nil, nil, err
	} else if len(params) > 0 {
		loadOpts = append(loadOpts, spec.WithParams(spec.QuoteRuntimeParams(params, nil)))
	}

	dag, err := spec.Load(ctx, workspace.dagFile, loadOpts...)
	if err != nil {
		cleanupFunc()
		return nil, nil, fmt.Errorf("failed to load action DAG from workspace: %w", err)
	}
	dag.SourceFile = task.SourceFile

	logger.Info(ctx, "Materialized action workspace",
		tag.Target(task.Target),
		tag.File(workspace.dagFile),
		slog.String("workspace", workspace.dir),
		slog.String("digest", workspace.desc.Digest))

	return dag, cleanupFunc, nil
}

// agentEnv holds temporary directories and cleanup function for agent execution.
type agentEnv struct {
	logDir      string
	logFile     string
	artifactDir string
	cleanup     func()
}

// createAgentEnv creates temporary directories for agent execution.
// The cleanup function must be called after execution completes.
// Includes workerID in path to prevent collisions with concurrent workers on the same host.
func (h *remoteTaskHandler) createAgentEnv(ctx context.Context, dag *core.DAG, dagRunID string) (*agentEnv, error) {
	logDir := filepath.Join(os.TempDir(), "dagu", "worker-logs", h.workerID, dagRunID)
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	artifactDir := ""
	if dag != nil && dag.ArtifactsEnabled() {
		var err error
		artifactDir, err = logpath.GenerateDir(
			ctx,
			filepath.Join(os.TempDir(), "dagu", "worker-artifacts", h.workerID),
			"",
			dag.Name,
			dagRunID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create artifact directory: %w", err)
		}
	}

	return &agentEnv{
		logDir:      logDir,
		logFile:     filepath.Join(logDir, "scheduler.log"),
		artifactDir: artifactDir,
		cleanup: func() {
			if err := os.RemoveAll(logDir); err != nil {
				logger.Warn(ctx, "Failed to cleanup temp log directory",
					slog.String("path", logDir),
					tag.Error(err))
			}
			if artifactDir != "" {
				if err := os.RemoveAll(artifactDir); err != nil {
					logger.Warn(ctx, "Failed to cleanup temp artifact directory",
						slog.String("path", artifactDir),
						tag.Error(err))
				}
			}
		},
	}, nil
}

func (h *remoteTaskHandler) executeDAGRun(
	ctx context.Context,
	dag *core.DAG,
	dagRunID string,
	attemptID string,
	attemptKey string,
	scheduleTime string,
	root exec.DAGRunRef,
	parent exec.DAGRunRef,
	owner exec.HostInfo,
	statusPusher runtime.StatusPusher,
	logStreamer runtime.SchedulerLogStreamer,
	artifactUploader runtime.ArtifactFinalizer,
	queuedRun bool,
	retry *retryConfig,
	extraEnvs []string,
	profileName string,
) error {
	// Create temporary directory for local operations
	env, err := h.createAgentEnv(ctx, dag, dagRunID)
	if err != nil {
		return newTaskInitError(err)
	}
	defer env.cleanup()

	// Open scheduler log file for writing
	logFile, err := os.OpenFile(env.logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return newTaskInitError(fmt.Errorf("failed to create scheduler log file: %w", err))
	}
	defer func() {
		if closeErr := logFile.Close(); closeErr != nil {
			logger.Warn(ctx, "Failed to close scheduler log file", tag.Error(closeErr))
		}
	}()

	statusPusher, schedulerFinalizer := withFinalSchedulerLogStatusPusher(statusPusher, logStreamer, env.logFile)

	// Create a scheduler log writer. Remote reporters keep scheduler logs local
	// and replay them before terminal status; other streamers may live-stream.
	var logWriter io.Writer = logFile
	if logStreamer != nil {
		streamingWriter := logStreamer.NewSchedulerLogWriter(ctx, logFile)
		defer func() {
			if closeErr := streamingWriter.Close(); closeErr != nil {
				logger.Warn(ctx, "Failed to close scheduler log streamer", tag.Error(closeErr))
			}
		}()
		logWriter = streamingWriter
	}

	// Configure logger to use the streaming writer
	ctx = logger.WithLogger(ctx, logger.NewLogger(logger.WithWriter(logWriter)))

	runtimeStores := h.runtimeStores

	toolEnvs, err := h.prepareDAGTools(ctx, dag)
	if err != nil {
		if schedulerFinalizer != nil && statusPusher != nil {
			target := dagRunID
			params := ""
			if dag != nil {
				target = dag.Name
				params = strings.Join(dag.Params, " ")
			}
			h.reportDAGRunInitFailure(ctx, target, dagRunID, attemptID, params, root, parent, statusPusher, err, profileName)
			return newReportedTaskInitError(err)
		}
		return newTaskInitError(err)
	}
	extraEnvs = append(extraEnvs, toolEnvs...)

	subWorkflowRunnerFactory := node.NewSubWorkflowRunnerFactory(node.SubWorkflowRunnerConfig{
		DAGRunMgr:         h.dagRunMgr,
		DAGStore:          h.dagStore,
		StateStore:        h.stateStore,
		SecretStore:       runtimeStores.SecretStore,
		ProfileStore:      runtimeStores.ProfileStore,
		ServiceRegistry:   h.serviceRegistry,
		PeerConfig:        h.peerConfig,
		DefaultExecMode:   h.config.DefaultExecMode,
		StatusPusher:      statusPusher,
		LogWriterFactory:  logStreamer,
		ArtifactFinalizer: artifactUploader,
		WorkerID:          h.workerID,
		DAGRunLogDir:      h.config.Paths.LogDir,
		DAGRunArtifactDir: h.config.Paths.ArtifactDir,
	})

	// Create a remote DAG loader that fetches DAG definitions from the coordinator
	// as a fallback when the local DAG store misses.
	remoteDAGLoader := rtagent.RemoteDAGLoader(func(ctx context.Context, name string) (*core.DAG, error) {
		dagYAML, err := h.coordinatorClient.GetDAG(ctx, name)
		if err != nil {
			return nil, err
		}
		if dagYAML == "" {
			return nil, nil
		}
		dag, loadErr := spec.LoadYAML(ctx, []byte(dagYAML), spec.WithName(name))
		if loadErr != nil {
			return nil, fmt.Errorf("failed to parse DAG from remote: %w", loadErr)
		}
		return dag, nil
	})

	// Build agent options
	opts := rtagent.Options{
		ParentDAGRun:             parent,
		WorkerID:                 h.workerID,
		StatusPusher:             statusPusher,
		LogWriterFactory:         logStreamer,
		ExtraEnvs:                extraEnvs,
		QueuedRun:                queuedRun,
		AttemptID:                attemptID,
		StateStore:               h.stateStore,
		SecretStore:              runtimeStores.SecretStore,
		SecretReferenceResolver:  h.secretReferenceResolver(dag, owner, coordinator.SecretReferenceRun{WorkerID: h.workerID, AttemptKey: attemptKey, AttemptID: attemptID}),
		ProfileStore:             runtimeStores.ProfileStore,
		ProfileName:              profileName,
		ServiceRegistry:          h.serviceRegistry,
		SubWorkflowRunnerFactory: subWorkflowRunnerFactory,
		RemoteDAGLoader:          remoteDAGLoader,
		RootDAGRun:               root,
		PeerConfig:               h.peerConfig,
		DefaultExecMode:          h.config.DefaultExecMode,
		ScheduleTime:             scheduleTime,
		ArtifactDir:              env.artifactDir,
		ArtifactFinalizer:        artifactUploader,
	}

	if retry != nil {
		opts.RetryTarget = retry.target
		opts.StepRetry = retry.stepName
		opts.TriggerType = retry.triggerType
	}

	// Create the agent
	agentInstance := rtagent.New(
		dagRunID,
		dag,
		env.logDir,
		env.logFile,
		h.dagRunMgr,
		h.dagStore,
		opts,
	)

	// Run the agent
	if err := agentInstance.Run(ctx); err != nil {
		logger.Error(ctx, "DAG execution failed",
			tag.RunID(dagRunID),
			tag.Error(err))
		return err
	}

	logger.Info(ctx, "DAG execution completed",
		tag.RunID(dagRunID))

	return nil
}

func (h *remoteTaskHandler) secretReferenceResolver(dag *core.DAG, owner exec.HostInfo, run coordinator.SecretReferenceRun) secrets.ReferenceResolver {
	client, ok := h.coordinatorClient.(coordinator.SecretReferenceClient)
	if !ok {
		return nil
	}
	workspaceName := ""
	if dag != nil {
		if name, found := exec.WorkspaceNameFromLabels(dag.Labels); found {
			workspaceName = name
		}
	}
	return coordinator.NewSecretReferenceResolver(client, workspaceName, owner, run)
}

func (h *remoteTaskHandler) prepareDAGTools(ctx context.Context, dag *core.DAG) ([]string, error) {
	workDir := ""
	if dag != nil {
		workDir = dag.WorkingDir
	}
	dataDir := ""
	toolsDir := ""
	if h.config != nil {
		dataDir = h.config.Paths.DataDir
		toolsDir = h.config.Paths.ToolsDir
	}
	return dagutools.PrepareDAG(ctx, dag, daguaqua.New(), dagutools.InstallOptions{
		ToolsDir: toolsDir,
		DataDir:  dataDir,
		WorkDir:  workDir,
	}, h.dagToolsBasePath())
}

func (h *remoteTaskHandler) dagToolsBasePath() string {
	if h.config != nil {
		for _, env := range h.config.Core.BaseEnv.AsSlice() {
			key, value, ok := strings.Cut(env, "=")
			if ok && strings.EqualFold(key, "PATH") {
				return value
			}
		}
	}
	return os.Getenv("PATH")
}

func previousStatusParams(task *coordinatorv1.Task) ([]string, error) {
	if task.Operation != coordinatorv1.Operation_OPERATION_RETRY || task.PreviousStatus == nil {
		return nil, nil
	}

	status, err := convert.ProtoToDAGRunStatus(task.PreviousStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to decode previous task status: %w", err)
	}

	return append([]string(nil), status.ParamsList...), nil
}
