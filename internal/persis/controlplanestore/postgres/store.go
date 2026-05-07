// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	cmncrypto "github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

var _ exec.DAGRunStore = (*Store)(nil)

// PoolConfig configures the PostgreSQL connection pool.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime int
	ConnMaxIdleTime int
}

// Config configures the PostgreSQL control-plane store.
type Config struct {
	DSN               string
	LocalWorkDirBase  string
	AutoMigrate       bool
	LatestStatusToday bool
	Location          *time.Location
	Pool              PoolConfig
	WebhookEncryptor  *cmncrypto.Encryptor
}

// Store persists control-plane data in PostgreSQL.
type Store struct {
	pool              *pgxpool.Pool
	queries           *db.Queries
	localWorkDirBase  string
	latestStatusToday bool
	location          *time.Location
	webhookEncryptor  *cmncrypto.Encryptor
	serviceMu         sync.Mutex
	services          map[exec.ServiceName]*postgresServiceRegistration
}

// DAGRuns returns the DAG-run sub-store implemented by this PostgreSQL control-plane store.
func (s *Store) DAGRuns() exec.DAGRunStore {
	return s
}

// New creates a PostgreSQL-backed control-plane store.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.DSN == "" {
		return nil, errors.New("postgres control-plane store DSN must not be empty")
	}
	if cfg.AutoMigrate {
		if err := RunMigrations(ctx, cfg.DSN); err != nil {
			return nil, err
		}
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres control-plane store DSN: %w", err)
	}
	applyPoolConfig(poolCfg, cfg.Pool)

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres control-plane store pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres control-plane store: %w", err)
	}

	location := cfg.Location
	if location == nil {
		location = time.Local
	}

	return &Store{
		pool:              pool,
		queries:           db.New(pool),
		localWorkDirBase:  cfg.LocalWorkDirBase,
		latestStatusToday: cfg.LatestStatusToday,
		location:          location,
		webhookEncryptor:  cfg.WebhookEncryptor,
		services:          make(map[exec.ServiceName]*postgresServiceRegistration),
	}, nil
}

// Close closes the underlying PostgreSQL connection pool.
func (s *Store) Close() error {
	if s != nil && s.pool != nil {
		s.Unregister(context.Background())
		s.pool.Close()
	}
	return nil
}

func applyPoolConfig(poolCfg *pgxpool.Config, cfg PoolConfig) {
	if cfg.MaxOpenConns > 0 {
		poolCfg.MaxConns = int32(cfg.MaxOpenConns) //nolint:gosec
	}
	if cfg.MaxIdleConns > 0 {
		minIdleConns := int32(cfg.MaxIdleConns) //nolint:gosec
		if poolCfg.MaxConns > 0 && minIdleConns > poolCfg.MaxConns {
			minIdleConns = poolCfg.MaxConns
		}
		poolCfg.MinIdleConns = minIdleConns
	}
	if cfg.ConnMaxLifetime > 0 {
		poolCfg.MaxConnLifetime = time.Duration(cfg.ConnMaxLifetime) * time.Second
	}
	if cfg.ConnMaxIdleTime > 0 {
		poolCfg.MaxConnIdleTime = time.Duration(cfg.ConnMaxIdleTime) * time.Second
	}
}

func (s *Store) CreateAttempt(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	if dagRunID == "" {
		return nil, errors.New("dag-run ID is empty")
	}
	if err := exec.ValidateDAGRunID(dagRunID); err != nil {
		return nil, err
	}
	if dag == nil {
		return nil, errors.New("DAG must not be nil")
	}
	if err := core.ValidateDAGName(dag.Name); err != nil {
		return nil, err
	}

	if opts.RootDAGRun != nil {
		return s.createSubAttempt(ctx, dag, timestamp, dagRunID, opts)
	}
	return s.createRootAttempt(ctx, dag, timestamp, dagRunID, opts)
}

func (s *Store) createRootAttempt(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	var row db.DaguDagRunAttempt
	err := s.withTx(ctx, func(q *db.Queries) error {
		lockKey := dagLockKey(dag.Name, dagRunID)
		if err := q.LockDAGRunKey(ctx, lockKey); err != nil {
			return fmt.Errorf("lock dag-run: %w", err)
		}

		run, findErr := q.FindRootRun(ctx, db.FindRootRunParams{
			DagName:  dag.Name,
			DagRunID: dagRunID,
		})
		if opts.Retry {
			if findErr != nil {
				if errors.Is(findErr, pgx.ErrNoRows) {
					return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRunID)
				}
				return findErr
			}
		} else if findErr == nil {
			return fmt.Errorf("%w: %s", exec.ErrDAGRunAlreadyExists, dagRunID)
		} else if !errors.Is(findErr, pgx.ErrNoRows) {
			return findErr
		} else {
			workspaceName, workspaceValid := workspaceFromLabels(dag.Labels)
			createdRun, err := s.createRun(ctx, q, dag.Name, dagRunID, exec.NewDAGRunRef(dag.Name, dagRunID), true, timestamp, workspaceName, workspaceValid)
			if err != nil {
				return err
			}
			run = createdRun
		}

		runCreatedAt := timestamp
		if findErr == nil {
			runCreatedAt = timeFromTimestamptz(run.RunCreatedAt)
		}

		created, err := s.insertAttempt(ctx, q, run.ID, dag, dagRunID, exec.NewDAGRunRef(dag.Name, dagRunID), true, runCreatedAt, timestamp, opts.AttemptID)
		if err != nil {
			return err
		}
		row = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) createSubAttempt(ctx context.Context, dag *core.DAG, timestamp time.Time, dagRunID string, opts exec.NewDAGRunAttemptOptions) (exec.DAGRunAttempt, error) {
	root := *opts.RootDAGRun
	if err := core.ValidateDAGName(root.Name); err != nil {
		return nil, err
	}
	if err := exec.ValidateDAGRunID(root.ID); err != nil {
		return nil, err
	}

	var row db.DaguDagRunAttempt
	err := s.withTx(ctx, func(q *db.Queries) error {
		if err := q.LockDAGRunKey(ctx, dagLockKey(root.Name, root.ID)); err != nil {
			return fmt.Errorf("lock root dag-run: %w", err)
		}
		if _, err := q.FindRootRun(ctx, db.FindRootRunParams{
			DagName:  root.Name,
			DagRunID: root.ID,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, root.ID)
			}
			return err
		}

		run, findErr := q.FindSubRun(ctx, db.FindSubRunParams{
			RootDagName:  root.Name,
			RootDagRunID: root.ID,
			DagRunID:     dagRunID,
		})
		if findErr != nil {
			if !errors.Is(findErr, pgx.ErrNoRows) {
				return findErr
			}
			workspaceName, workspaceValid := workspaceFromLabels(dag.Labels)
			createdRun, err := s.createRun(ctx, q, dag.Name, dagRunID, root, false, timestamp, workspaceName, workspaceValid)
			if err != nil {
				return err
			}
			run = createdRun
		}

		runCreatedAt := timestamp
		if findErr == nil {
			runCreatedAt = timeFromTimestamptz(run.RunCreatedAt)
		}

		created, err := s.insertAttempt(ctx, q, run.ID, dag, dagRunID, root, false, runCreatedAt, timestamp, opts.AttemptID)
		if err != nil {
			return err
		}
		row = created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) createRun(
	ctx context.Context,
	q *db.Queries,
	dagName string,
	dagRunID string,
	root exec.DAGRunRef,
	isRoot bool,
	runCreatedAt time.Time,
	workspace sql.NullString,
	workspaceValid bool,
) (db.DaguDagRun, error) {
	rowID, err := uuid.NewV7()
	if err != nil {
		return db.DaguDagRun{}, err
	}
	return q.CreateRun(ctx, db.CreateRunParams{
		ID:             rowID,
		DagName:        dagName,
		DagRunID:       dagRunID,
		RootDagName:    root.Name,
		RootDagRunID:   root.ID,
		IsRoot:         isRoot,
		RunCreatedAt:   timestamptz(runCreatedAt),
		Workspace:      workspace,
		WorkspaceValid: workspaceValid,
	})
}

func (s *Store) insertAttempt(
	ctx context.Context,
	q *db.Queries,
	runID uuid.UUID,
	dag *core.DAG,
	dagRunID string,
	root exec.DAGRunRef,
	isRoot bool,
	runCreatedAt time.Time,
	attemptCreatedAt time.Time,
	attemptID string,
) (db.DaguDagRunAttempt, error) {
	rowID, err := uuid.NewV7()
	if err != nil {
		return db.DaguDagRunAttempt{}, err
	}
	if attemptID == "" {
		attemptID, err = genAttemptID()
		if err != nil {
			return db.DaguDagRunAttempt{}, err
		}
	}

	dagData, err := marshalOptionalDAG(dag)
	if err != nil {
		return db.DaguDagRunAttempt{}, err
	}
	workspaceName, workspaceValid := workspaceFromLabels(dag.Labels)
	workDir := s.workDir(root, dagRunID)

	return q.CreateAttempt(ctx, db.CreateAttemptParams{
		ID:               rowID,
		RunID:            runID,
		DagName:          dag.Name,
		DagRunID:         dagRunID,
		RootDagName:      root.Name,
		RootDagRunID:     root.ID,
		IsRoot:           isRoot,
		AttemptID:        attemptID,
		RunCreatedAt:     timestamptz(runCreatedAt),
		AttemptCreatedAt: timestamptz(attemptCreatedAt),
		Workspace:        workspaceName,
		WorkspaceValid:   workspaceValid,
		DagData:          dagData,
		LocalWorkDir:     workDir,
	})
}

func (s *Store) RecentAttempts(ctx context.Context, name string, itemLimit int) []exec.DAGRunAttempt {
	if itemLimit <= 0 {
		itemLimit = 10
	}
	rows, err := s.queries.RecentAttemptsByName(ctx, db.RecentAttemptsByNameParams{
		DagName:   name,
		ItemLimit: int32(itemLimit), //nolint:gosec
	})
	if err != nil {
		logger.Warn(ctx, "postgres dag-run store: recent attempts query failed",
			tag.Error(err),
			slog.String("dag", name),
		)
		return nil
	}

	attempts := make([]exec.DAGRunAttempt, 0, len(rows))
	for _, row := range rows {
		attempt, err := s.attemptFromRow(row)
		if err != nil {
			logger.Warn(ctx, "postgres dag-run store: failed to decode recent attempt; skipping",
				tag.Error(err),
				slog.String("dag", row.DagName),
				slog.String("dag_run_id", row.DagRunID),
			)
			continue
		}
		attempts = append(attempts, attempt)
	}
	return attempts
}

func (s *Store) LatestAttempt(ctx context.Context, name string) (exec.DAGRunAttempt, error) {
	hasFrom := false
	fromAt := pgtype.Timestamptz{}
	if s.latestStatusToday {
		now := time.Now().In(s.location)
		startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.location)
		hasFrom = true
		fromAt = timestamptz(startOfDay)
	}

	row, err := s.queries.LatestAttemptByName(ctx, db.LatestAttemptByNameParams{
		DagName: name,
		HasFrom: hasFrom,
		FromAt:  fromAt,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, exec.ErrNoStatusData
		}
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) FindAttempt(ctx context.Context, dagRun exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	if dagRun.ID == "" {
		return nil, errors.New("dag-run ID is empty")
	}
	row, err := s.latestRootAttempt(ctx, dagRun)
	if err != nil {
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) latestRootAttempt(ctx context.Context, dagRun exec.DAGRunRef) (db.DaguDagRunAttempt, error) {
	row, err := s.queries.LatestRootAttempt(ctx, db.LatestRootAttemptParams{
		DagName:  dagRun.Name,
		DagRunID: dagRun.ID,
	})
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.DaguDagRunAttempt{}, err
	}
	if _, anyErr := s.queries.FindRootRun(ctx, db.FindRootRunParams{
		DagName:  dagRun.Name,
		DagRunID: dagRun.ID,
	}); anyErr == nil {
		return db.DaguDagRunAttempt{}, exec.ErrNoStatusData
	}
	return db.DaguDagRunAttempt{}, fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRun.ID)
}

func (s *Store) FindSubAttempt(ctx context.Context, dagRun exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	if dagRun.ID == "" {
		return nil, errors.New("dag-run ID is empty")
	}
	row, err := s.queries.LatestSubAttempt(ctx, db.LatestSubAttemptParams{
		RootDagName:  dagRun.Name,
		RootDagRunID: dagRun.ID,
		DagRunID:     subDAGRunID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, subDAGRunID)
		}
		return nil, err
	}
	return s.attemptFromRow(row)
}

func (s *Store) CreateSubAttempt(ctx context.Context, rootRef exec.DAGRunRef, subDAGRunID string) (exec.DAGRunAttempt, error) {
	dag := &core.DAG{Name: rootRef.Name}
	return s.createSubAttempt(ctx, dag, time.Now(), subDAGRunID, exec.NewDAGRunAttemptOptions{
		RootDAGRun: &rootRef,
	})
}

func (s *Store) CompareAndSwapLatestAttemptStatus(
	ctx context.Context,
	dagRun exec.DAGRunRef,
	expectedAttemptID string,
	expectedStatus core.Status,
	mutate func(*exec.DAGRunStatus) error,
) (*exec.DAGRunStatus, bool, error) {
	var result *exec.DAGRunStatus
	var swapped bool
	err := s.withTx(ctx, func(q *db.Queries) error {
		if err := q.LockDAGRunKey(ctx, dagLockKey(dagRun.Name, dagRun.ID)); err != nil {
			return err
		}
		row, err := q.LatestRootAttemptForUpdate(ctx, db.LatestRootAttemptForUpdateParams{
			DagName:  dagRun.Name,
			DagRunID: dagRun.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return exec.ErrNoStatusData
			}
			return err
		}
		status, err := statusFromRow(row)
		if err != nil {
			return err
		}
		result = status
		if expectedAttemptID != "" && status.AttemptID != expectedAttemptID {
			return nil
		}
		if status.Status != expectedStatus {
			return nil
		}
		if err := mutate(status); err != nil {
			return err
		}
		if err := updateStatus(ctx, q, row.ID, *status); err != nil {
			return err
		}
		result = status
		swapped = true
		return nil
	})
	return result, swapped, err
}

func (s *Store) ListStatuses(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) ([]*exec.DAGRunStatus, error) {
	options, err := prepareListOptions(opts)
	if err != nil {
		return nil, err
	}
	page, err := s.listStatuses(ctx, options, options.Limit, false)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *Store) ListStatusesPage(ctx context.Context, opts ...exec.ListDAGRunStatusesOption) (exec.DAGRunStatusPage, error) {
	options, err := prepareListOptions(opts)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return s.listStatuses(ctx, options, options.Limit, true)
}

func (s *Store) listStatuses(ctx context.Context, opts exec.ListDAGRunStatusesOptions, limit int, returnCursor bool) (exec.DAGRunStatusPage, error) {
	cursorKey, err := decodeQueryCursor(opts.Cursor, opts)
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}

	target := limit
	if target <= 0 {
		if opts.Unlimited {
			target = math.MaxInt
		} else {
			target = opts.Limit
		}
	}
	if target <= 0 {
		target = 1
	}
	need := target
	if returnCursor && target < math.MaxInt {
		need++
	}

	labelFilters := make([]core.LabelFilter, 0, len(opts.Labels))
	for _, label := range opts.Labels {
		if trimmed := strings.TrimSpace(label); trimmed != "" {
			labelFilters = append(labelFilters, core.ParseLabelFilter(trimmed))
		}
	}

	items := make([]*exec.DAGRunStatus, 0, min(need, 1000))
	keys := make([]listKey, 0, cap(items))
	internalCursor := cursorKey
	cursorSet := opts.Cursor != ""
	chunkLimit := need
	if len(labelFilters) > 0 && chunkLimit < 1000 {
		chunkLimit = 1000
	}
	if chunkLimit <= 0 || chunkLimit == math.MaxInt {
		chunkLimit = 1000
	}

	for len(items) < need {
		rows, err := s.queries.ListRootStatusRows(ctx, s.listParams(opts, internalCursor, cursorSet, chunkLimit))
		if err != nil {
			return exec.DAGRunStatusPage{}, err
		}
		if len(rows) == 0 {
			break
		}

		for _, row := range rows {
			key := listKey{
				Timestamp: timeFromTimestamptz(row.RunCreatedAt).UTC(),
				Name:      row.DagName,
				DAGRunID:  row.DagRunID,
			}
			internalCursor = key
			cursorSet = true

			status, err := statusFromListRow(row)
			if err != nil {
				if !errors.Is(err, exec.ErrNoStatusData) {
					logger.Warn(ctx, "postgres dag-run store: failed to decode status row; skipping",
						tag.Error(err),
						slog.String("dag", row.DagName),
						slog.String("dag_run_id", row.DagRunID),
					)
				}
				continue
			}
			if len(labelFilters) > 0 && !core.NewLabels(status.Labels).MatchesFilters(labelFilters) {
				continue
			}
			items = append(items, status)
			keys = append(keys, key)
			if len(items) >= need {
				break
			}
		}
		if len(rows) < chunkLimit {
			break
		}
	}

	if !returnCursor || limit <= 0 || len(items) <= limit {
		return exec.DAGRunStatusPage{Items: items}, nil
	}
	nextCursor, err := encodeQueryCursor(opts, keys[limit-1])
	if err != nil {
		return exec.DAGRunStatusPage{}, err
	}
	return exec.DAGRunStatusPage{
		Items:      items[:limit],
		NextCursor: nextCursor,
	}, nil
}

func (s *Store) listParams(opts exec.ListDAGRunStatusesOptions, cursor listKey, cursorSet bool, pageLimit int) db.ListRootStatusRowsParams {
	statuses := make([]int32, 0, len(opts.Statuses))
	for _, status := range opts.Statuses {
		statuses = append(statuses, int32(status)) //nolint:gosec
	}

	params := db.ListRootStatusRowsParams{
		ExactName:              opts.ExactName,
		NameContains:           opts.Name,
		DagRunIDContains:       opts.DAGRunID,
		HasFrom:                !opts.From.IsZero(),
		FromAt:                 timestamptz(opts.From.Time),
		HasTo:                  !opts.To.IsZero(),
		ToAt:                   timestamptz(opts.To.Time),
		Statuses:               statuses,
		WorkspaceFilterEnabled: opts.WorkspaceFilter != nil && opts.WorkspaceFilter.Enabled,
		PageLimit:              int32(pageLimit), //nolint:gosec
		CursorSet:              cursorSet,
		CursorTimestamp:        timestamptz(cursor.Timestamp),
		CursorName:             cursor.Name,
		CursorDagRunID:         cursor.DAGRunID,
	}
	if opts.WorkspaceFilter != nil {
		params.IncludeUnlabelled = opts.WorkspaceFilter.IncludeUnlabelled
		params.Workspaces = append([]string(nil), opts.WorkspaceFilter.Workspaces...)
	}
	return params
}

func (s *Store) RemoveOldDAGRuns(ctx context.Context, name string, retentionDays int, opts ...exec.RemoveOldDAGRunsOption) ([]string, error) {
	var options exec.RemoveOldDAGRunsOptions
	for _, opt := range opts {
		opt(&options)
	}

	var runIDs []string
	var err error
	if options.RetentionRuns != nil {
		retentionRuns := *options.RetentionRuns
		if retentionRuns <= 0 {
			return nil, nil
		}
		ids, listErr := s.queries.ListRemovableRunsByCount(ctx, db.ListRemovableRunsByCountParams{
			DagName:        name,
			ActiveStatuses: activeStatusInts(),
			RetentionRuns:  int32(retentionRuns), //nolint:gosec
		})
		err = listErr
		runIDs = uniqueStrings(ids)
	} else {
		if retentionDays < 0 {
			return nil, nil
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
		ids, listErr := s.queries.ListRemovableRunsByDays(ctx, db.ListRemovableRunsByDaysParams{
			DagName:        name,
			ActiveStatuses: activeStatusInts(),
			Cutoff:         timestamptz(cutoff),
		})
		err = listErr
		runIDs = uniqueStrings(ids)
	}
	if err != nil {
		return nil, err
	}
	if options.DryRun {
		return runIDs, nil
	}
	for _, runID := range runIDs {
		if err := s.RemoveDAGRun(ctx, exec.NewDAGRunRef(name, runID)); err != nil {
			return nil, err
		}
	}
	return runIDs, nil
}

func (s *Store) RenameDAGRuns(ctx context.Context, oldName, newName string) error {
	return s.queries.RenameDAGRuns(ctx, db.RenameDAGRunsParams{
		OldName: oldName,
		NewName: newName,
	})
}

func (s *Store) RemoveDAGRun(ctx context.Context, dagRun exec.DAGRunRef, opts ...exec.RemoveDAGRunOption) error {
	if dagRun.ID == "" {
		return errors.New("dag-run ID is empty")
	}

	var options exec.RemoveDAGRunOptions
	for _, opt := range opts {
		opt(&options)
	}

	var deleted []string
	if err := s.withTx(ctx, func(q *db.Queries) error {
		if err := q.LockDAGRunKey(ctx, dagLockKey(dagRun.Name, dagRun.ID)); err != nil {
			return err
		}
		if options.RejectActive {
			row, err := q.LatestRootAttemptForUpdate(ctx, db.LatestRootAttemptForUpdateParams{
				DagName:  dagRun.Name,
				DagRunID: dagRun.ID,
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRun.ID)
				}
				return err
			}
			status, err := statusFromRow(row)
			if err != nil {
				return err
			}
			if status.Status.IsActive() {
				return fmt.Errorf("%w: %s", exec.ErrDAGRunActive, status.Status.String())
			}
		}
		ids, err := q.DeleteDAGRunRows(ctx, db.DeleteDAGRunRowsParams{
			RootDagName:  dagRun.Name,
			RootDagRunID: dagRun.ID,
		})
		deleted = ids
		return err
	}); err != nil {
		return err
	}
	if len(deleted) == 0 {
		return fmt.Errorf("%w: %s", exec.ErrDAGRunIDNotFound, dagRun.ID)
	}

	if s.localWorkDirBase != "" {
		_ = os.RemoveAll(s.runWorkDir(dagRun))
	}
	return nil
}

func prepareListOptions(opts []exec.ListDAGRunStatusesOption) (exec.ListDAGRunStatusesOptions, error) {
	var options exec.ListDAGRunStatusesOptions
	for _, opt := range opts {
		opt(&options)
	}
	if !options.AllHistory && options.From.IsZero() && options.To.IsZero() {
		options.From = exec.NewUTC(time.Now().Truncate(24 * time.Hour))
	}
	if !options.Unlimited {
		const maxLimit = 1000
		if options.Limit == 0 || options.Limit > maxLimit {
			options.Limit = maxLimit
		}
	}
	return options, nil
}

func (s *Store) withTx(ctx context.Context, fn func(*db.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(s.queries.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) attemptFromRow(row db.DaguDagRunAttempt) (*Attempt, error) {
	return newAttempt(s.queries, row)
}

func statusFromListRow(row db.DaguDagRun) (*exec.DAGRunStatus, error) {
	return statusFromJSON(row.StatusData)
}

func statusFromRow(row db.DaguDagRunAttempt) (*exec.DAGRunStatus, error) {
	return statusFromJSON(row.StatusData)
}

func statusFromJSON(data []byte) (*exec.DAGRunStatus, error) {
	if len(data) == 0 {
		return nil, exec.ErrNoStatusData
	}
	return exec.StatusFromJSON(string(data))
}

func updateStatus(ctx context.Context, q *db.Queries, id uuid.UUID, status exec.DAGRunStatus) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	workspaceName, workspaceValid := workspaceFromLabels(core.NewLabels(status.Labels))
	return q.UpdateAttemptStatus(ctx, db.UpdateAttemptStatusParams{
		ID:             id,
		StatusData:     data,
		Status:         int32(status.Status), //nolint:gosec
		Workspace:      workspaceName,
		WorkspaceValid: workspaceValid,
		StartedAt:      parseStatusTime(status.StartedAt),
		FinishedAt:     parseStatusTime(status.FinishedAt),
	})
}

func marshalOptionalDAG(dag *core.DAG) ([]byte, error) {
	if dag == nil {
		return nil, nil
	}
	data, err := json.Marshal(dag)
	if err != nil {
		return nil, fmt.Errorf("marshal DAG: %w", err)
	}
	return data, nil
}

func workspaceFromLabels(labels core.Labels) (sql.NullString, bool) {
	workspaceName, state := exec.WorkspaceLabelFromLabels(labels)
	switch state {
	case exec.WorkspaceLabelValid:
		return sql.NullString{String: workspaceName, Valid: true}, true
	case exec.WorkspaceLabelMissing:
		return sql.NullString{}, true
	case exec.WorkspaceLabelInvalid:
		return sql.NullString{}, false
	default:
		return sql.NullString{}, false
	}
}

func parseStatusTime(value string) pgtype.Timestamptz {
	parsed, err := stringutil.ParseTime(value)
	if err != nil || parsed.IsZero() {
		return pgtype.Timestamptz{}
	}
	return timestamptz(parsed)
}

func timestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func timeFromTimestamptz(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

func dagLockKey(name, runID string) string {
	return name + ":" + runID
}

func (s *Store) workDir(root exec.DAGRunRef, dagRunID string) string {
	rootDir := s.runWorkDir(root)
	if rootDir == "" {
		return ""
	}
	return filepath.Join(rootDir, dagRunID, "work")
}

func (s *Store) runWorkDir(root exec.DAGRunRef) string {
	if s.localWorkDirBase == "" {
		return ""
	}
	return filepath.Join(s.localWorkDirBase, "postgres-work", root.Name, root.ID)
}

func genAttemptID() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random attempt ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func activeStatusInts() []int32 {
	return []int32{int32(core.Running), int32(core.Queued), int32(core.Waiting)}
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
