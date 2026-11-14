// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

const (
	commitDirName  = "writes"
	commitFileExt  = ".json"
	commitFileMode = 0o600
	commitDirMode  = 0o700
	// commitLockTimeout bounds the wait for another run to finish changing
	// a DAG's commit points.
	commitLockTimeout = 5 * time.Second
)

// CommitPoint is an operation of a step that sends a write, and the kind
// of write it sends. A rehearsal or a blocked write notes one; once
// approved, a step running in record mode sends that write from that
// operation and no other.
type CommitPoint struct {
	// Key identifies the operation as the step's replay cache does: its
	// position, words, and page.
	Key string `json:"key"`
	// Op is the operation's position in with.do, Operation its words, and
	// Page the page it ran on.
	Op        int    `json:"op"`
	Operation string `json:"operation"`
	Page      string `json:"page,omitempty"`
	Signature string `json:"signature"`
	Approved  bool   `json:"approved"`
	// Actions are what the operation did on the page, recorded as the
	// browser runtime replays them; an approved commit act replays them
	// and asks no model.
	Actions json.RawMessage `json:"actions,omitempty"`
	// Example is the write as it was last seen.
	Example    *Event     `json:"example,omitempty"`
	SeenAt     time.Time  `json:"seen_at"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

// CommitStore keeps the commit points of each DAG's steps under the
// browser data directory, apart from the replay cache, which a run may
// drop.
type CommitStore struct {
	dir string
}

// NewCommitStore returns the commit points kept under the browser data
// directory.
func NewCommitStore(browserDataDir string) *CommitStore {
	return &CommitStore{dir: filepath.Join(browserDataDir, commitDirName)}
}

func (s *CommitStore) path(dag, step string) string {
	return filepath.Join(s.dir, fileutil.SafeName(dag), fileutil.SafeName(step)+commitFileExt)
}

// Load returns the commit points of a step; none when it has no file.
func (s *CommitStore) Load(dag, step string) ([]CommitPoint, error) {
	data, err := os.ReadFile(s.path(dag, step))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var points []CommitPoint
	if err := json.Unmarshal(data, &points); err != nil {
		return nil, fmt.Errorf("read the commit points of %s/%s: %w", dag, step, err)
	}
	return points, nil
}

// Steps returns the steps of a DAG that have commit points, in the
// file-safe form they are kept under.
func (s *CommitStore) Steps(dag string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, fileutil.SafeName(dag)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var steps []string
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), commitFileExt); ok && !entry.IsDir() {
			steps = append(steps, name)
		}
	}
	slices.Sort(steps)
	return steps, nil
}

// Note keeps the commit points seen, merged with those kept: a point
// already kept, by key and signature, keeps its approval and takes the
// newer example and actions.
func (s *CommitStore) Note(ctx context.Context, dag, step string, seen ...CommitPoint) error {
	return s.update(ctx, dag, step, func(points []CommitPoint) []CommitPoint {
		for _, point := range seen {
			i := slices.IndexFunc(points, func(p CommitPoint) bool { return p.Key == point.Key && p.Signature == point.Signature })
			if i < 0 {
				points = append(points, point)
				continue
			}
			approved, approvedAt := points[i].Approved, points[i].ApprovedAt
			actions := point.Actions
			if len(actions) == 0 {
				actions = points[i].Actions
			}
			points[i] = point
			points[i].Approved, points[i].ApprovedAt, points[i].Actions = approved || point.Approved, approvedAt, actions
		}
		return points
	})
}

// Approve marks the points match accepts approved, adding those of from
// that are not kept yet, and returns them.
func (s *CommitStore) Approve(ctx context.Context, dag, step string, from []CommitPoint, match func(CommitPoint) bool, now time.Time) ([]CommitPoint, error) {
	var approved []CommitPoint
	err := s.update(ctx, dag, step, func(points []CommitPoint) []CommitPoint {
		for _, point := range from {
			if !slices.ContainsFunc(points, func(p CommitPoint) bool { return p.Key == point.Key && p.Signature == point.Signature }) {
				points = append(points, point)
			}
		}
		for i := range points {
			if !match(points[i]) {
				continue
			}
			if !points[i].Approved {
				points[i].Approved, points[i].ApprovedAt = true, &now
			}
			approved = append(approved, points[i])
		}
		return points
	})
	return approved, err
}

// Revoke withdraws the approval of the points match accepts and returns
// them.
func (s *CommitStore) Revoke(ctx context.Context, dag, step string, match func(CommitPoint) bool) ([]CommitPoint, error) {
	var revoked []CommitPoint
	err := s.update(ctx, dag, step, func(points []CommitPoint) []CommitPoint {
		for i := range points {
			if points[i].Approved && match(points[i]) {
				points[i].Approved, points[i].ApprovedAt = false, nil
				revoked = append(revoked, points[i])
			}
		}
		return points
	})
	return revoked, err
}

// update changes a step's commit points under the lock of its DAG, which
// the runs of the DAG and the commands that approve share.
func (s *CommitStore) update(ctx context.Context, dag, step string, change func([]CommitPoint) []CommitPoint) error {
	path := s.path(dag, step)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, commitDirMode); err != nil {
		return fmt.Errorf("create the commit points directory: %w", err)
	}
	lockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), commitLockTimeout)
	defer cancel()
	lock := dirlock.New(dir, nil)
	if err := lock.Lock(lockCtx); err != nil {
		return fmt.Errorf("lock the commit points: %w", err)
	}
	defer func() { _ = lock.Unlock() }()
	points, err := s.Load(dag, step)
	if err != nil {
		return err
	}
	return fileutil.WriteJSONAtomic(path, change(points), commitFileMode)
}

// Allowed returns the signatures the approved points of an operation send.
func Allowed(points []CommitPoint, key string) []string {
	var signatures []string
	for _, point := range points {
		if point.Approved && point.Key == key {
			signatures = append(signatures, point.Signature)
		}
	}
	return signatures
}
