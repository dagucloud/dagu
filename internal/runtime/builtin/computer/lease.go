// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	desktopLockName = "desktop.lock"
	desktopDirMode  = 0o700
	// lastInputFile holds when the step that last held the desktop sent
	// input, so the next step does not take that input for a person's.
	lastInputFile     = "last-input"
	lastInputFileMode = 0o600
	// workAppFile holds the application the last step of a DAG run worked
	// in, with the run's ID, so the run's next step can bring it back after
	// a person used the desktop in between.
	workAppFile = "work-app"
	// appLocksDir holds a lock for each application a step works in.
	appLocksDir = "apps"
)

// desktopLease holds a lock steps on one host share. The desktop lock
// gives one step at a time the pointer, the keyboard, and the foreground,
// since two steps sending input at once would interfere; an application's
// lock gives one step at a time that application, since two steps working
// it share its windows.
type desktopLease struct {
	dir  string
	lock dirlock.DirLock
	stop context.CancelFunc
}

// userDesktopLock returns the lock shared by every Dagu process of the user,
// whatever its data directory, since they all operate the same desktop. It
// is empty when the user has no cache directory.
func userDesktopLock() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "dagu", computerhost.DataDirName, desktopLockName)
}

// acquireDesktop waits for the desktop lock in lockDir.
func acquireDesktop(ctx context.Context, lockDir string, t *agentstep.Timeline) (*desktopLease, error) {
	lease, err := acquireLock(ctx, lockDir, func() {
		t.Waiting(waitReasonDesktop, "Waiting for another computer step to finish using the desktop")
	})
	if err != nil {
		return nil, fmt.Errorf("lock the desktop: %w", err)
	}
	return lease, nil
}

// acquireApp waits for the lock of app beside the desktop lock in lockDir.
func acquireApp(ctx context.Context, lockDir, app string, t *agentstep.Timeline) (*desktopLease, error) {
	lease, err := acquireLock(ctx, filepath.Join(lockDir, appLocksDir, appLockName(app)), func() {
		t.Waiting(waitReasonApp, fmt.Sprintf("Waiting for another computer step to finish working in %s", app))
	})
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", app, err)
	}
	return lease, nil
}

func acquireLock(ctx context.Context, dir string, onWait func()) (*desktopLease, error) {
	if err := os.MkdirAll(dir, desktopDirMode); err != nil {
		return nil, err
	}
	lock := dirlock.New(dir, &dirlock.LockOptions{OnWait: onWait})
	if err := lock.Lock(ctx); err != nil {
		return nil, err
	}
	stop := agentstep.KeepLockAlive(ctx, lock)
	return &desktopLease{dir: dir, lock: lock, stop: stop}, nil
}

// tryLock takes the lock in dir unless another holder has it, waiting for
// nothing.
func tryLock(ctx context.Context, dir string) (*desktopLease, bool) {
	if err := os.MkdirAll(dir, desktopDirMode); err != nil {
		return nil, false
	}
	lock := dirlock.New(dir, nil)
	if lock.TryLock() != nil {
		return nil, false
	}
	return &desktopLease{dir: dir, lock: lock, stop: agentstep.KeepLockAlive(ctx, lock)}, true
}

// appLockName names an application's lock, as applications compare,
// ignoring case, with every character a file name may not hold replaced. A
// name of dots alone is prefixed, since it would name a directory above the
// lock once joined to it.
func appLockName(app string) string {
	name := strings.Map(func(r rune) rune {
		if r = foldRune(r); unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, app)
	if strings.Trim(name, ".") == "" {
		return "_" + name
	}
	return name
}

// foldRune maps a rune to the least of the runes strings.EqualFold takes for
// it, so application names that compare equal name one lock.
func foldRune(r rune) rune {
	least := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		least = min(least, f)
	}
	return least
}

// lastInput returns when the previous holder of the desktop last sent input,
// or the zero time.
func (l *desktopLease) lastInput() time.Time {
	data, err := os.ReadFile(filepath.Join(l.dir, lastInputFile)) //nolint:gosec // the lock directory is Dagu's own
	if err != nil {
		return time.Time{}
	}
	nanos, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

// recordInput notes when this holder last sent input, for the next one.
func (l *desktopLease) recordInput(at time.Time) {
	if at.IsZero() {
		return
	}
	_ = os.WriteFile(filepath.Join(l.dir, lastInputFile), []byte(strconv.FormatInt(at.UnixNano(), 10)), lastInputFileMode)
}

// readWorkApp returns the application the run's previous step last worked
// in, from the desktop lock in lockDir, or "" when a step of another run
// finished since.
func readWorkApp(lockDir, runID string) string {
	data, err := os.ReadFile(filepath.Join(lockDir, workAppFile)) //nolint:gosec // the lock directory is Dagu's own
	if err != nil {
		return ""
	}
	id, app, ok := strings.Cut(string(data), "\n")
	if !ok || runID == "" || id != runID {
		return ""
	}
	return app
}

// writeWorkApp notes the application this step last worked in, for the
// run's next step. An empty one still records the run, so an application
// an earlier run worked in is not brought back after this run's step. Steps
// that finish at once replace the note whole, so the next step reads one of
// them.
func writeWorkApp(lockDir, runID, app string) {
	if runID == "" {
		return
	}
	if err := os.MkdirAll(lockDir, desktopDirMode); err != nil {
		return
	}
	_ = fileutil.WriteFileAtomic(filepath.Join(lockDir, workAppFile), []byte(runID+"\n"+app), lastInputFileMode)
}

func (l *desktopLease) release() {
	if l == nil {
		return
	}
	l.stop()
	_ = l.lock.Unlock()
}

// takeDesktop makes the step hold the desktop until releaseDesktop, waiting
// while another step holds it: from the look at the screen its input is
// based on until the input is sent, which a step does only while it holds
// the desktop. app, the application the step works in meanwhile, is taken
// first, so a step never waits for an application while it holds the
// desktop: one that holds it and needs app another step holds lets the
// desktop go, waits for app, and takes the desktop again, unless a button
// it pressed is still down. Its callers call it before the look their
// input is based on. Neither wait counts toward the operation's time.
func (r *run) takeDesktop(ctx context.Context, app string) error {
	if r.lease != nil {
		if err := r.takeApp(ctx, app); err != nil || r.holdsApp(app) || r.driver.Holding() {
			return err
		}
		r.releaseDesktop()
	}
	if err := r.takeApp(ctx, app); err != nil {
		return err
	}
	defer r.pauseClock()()
	lease, err := acquireDesktop(ctx, r.lockDir, r.timeline)
	if err != nil {
		return err
	}
	r.lease = lease
	// Input another step sent before this one took the desktop is not a
	// person's.
	r.driver.AssumeInputSent(lease.lastInput())
	return nil
}

// tryDesktop takes the desktop unless another step holds it, waiting for
// nothing, and reports whether the step holds it.
func (r *run) tryDesktop(ctx context.Context) bool {
	if r.lease == nil {
		r.lease, _ = tryLock(ctx, r.lockDir)
	}
	return r.lease != nil
}

// releaseDesktop lets other steps use the desktop, noting when this step
// last sent input.
func (r *run) releaseDesktop() {
	if r.lease == nil {
		return
	}
	r.lease.recordInput(r.driver.InputSentAt())
	r.lease.release()
	r.lease = nil
}

// holdsApp reports whether the step holds app, or app names none.
func (r *run) holdsApp(app string) bool {
	return app == "" || (r.app != nil && strings.EqualFold(r.appName, app))
}

// takeApp makes the step hold app, the one it works in now, letting go of
// the one it held before, so a step holds one application at a time. A
// step holding the desktop waits for nothing, so it takes app only when no
// other step holds it, and keeps the one it holds otherwise.
func (r *run) takeApp(ctx context.Context, app string) error {
	if r.holdsApp(app) {
		return nil
	}
	if r.lease != nil {
		if lease, ok := tryLock(ctx, filepath.Join(r.lockDir, appLocksDir, appLockName(app))); ok {
			r.app.release()
			r.app, r.appName = lease, app
		}
		return nil
	}
	r.app.release()
	r.app = nil
	defer r.pauseClock()()
	lease, err := acquireApp(ctx, r.lockDir, app, r.timeline)
	if err != nil {
		return err
	}
	r.app, r.appName = lease, app
	return nil
}
