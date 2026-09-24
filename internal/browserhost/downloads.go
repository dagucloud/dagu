// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Download states reported by the browser.
const (
	downloadCompleted = "completed"
	downloadCanceled  = "canceled"
)

const defaultDownloadName = "download"

// Download is a file the browser finished saving.
type Download struct {
	Name string
	Path string
}

// DownloadWatcher saves the browser's downloads into a directory and tracks
// which are still in progress.
type DownloadWatcher struct {
	conn    *websocket.Conn
	dir     string
	cancel  context.CancelFunc
	done    chan struct{}
	changed chan struct{}

	mu        sync.Mutex
	started   map[string]string // guid → suggested file name
	completed []Download
	failed    []string
	readErr   error
}

// DenyDownloads makes the browser at cdpURL refuse every download.
func DenyDownloads(ctx context.Context, cdpURL string) error {
	return call(ctx, cdpURL, "Browser.setDownloadBehavior", map[string]any{"behavior": "deny"}, nil)
}

// WatchDownloads saves downloads from the browser at cdpURL into dir until
// the watcher is closed.
func WatchDownloads(ctx context.Context, cdpURL, dir string) (*DownloadWatcher, error) {
	conn, err := dialBrowser(ctx, cdpURL)
	if err != nil {
		return nil, err
	}

	readCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w := &DownloadWatcher{
		conn:    conn,
		dir:     dir,
		cancel:  cancel,
		done:    make(chan struct{}),
		changed: make(chan struct{}, 1),
		started: map[string]string{},
	}
	acknowledged := make(chan error, 1)
	go w.read(readCtx, acknowledged)

	// Downloads are saved under their guid and renamed when they finish, so
	// the saved name is known exactly.
	const requestID = 1
	message, _ := json.Marshal(map[string]any{
		"id":     requestID,
		"method": "Browser.setDownloadBehavior",
		"params": map[string]any{"behavior": "allowAndName", "downloadPath": dir, "eventsEnabled": true},
	})
	if err := conn.Write(ctx, websocket.MessageText, message); err != nil {
		_ = w.Close()
		return nil, err
	}
	select {
	case err := <-acknowledged:
		if err != nil {
			_ = w.Close()
			return nil, err
		}
	case <-ctx.Done():
		_ = w.Close()
		return nil, ctx.Err()
	}
	return w, nil
}

// Wait returns the downloads completed since the previous call. It first
// allows grace for a download to begin, then waits until none is in
// progress. It fails when a download was canceled or is still running when
// timeout passes.
func (w *DownloadWatcher) Wait(ctx context.Context, grace, timeout time.Duration) ([]Download, error) {
	graceEnds := time.Now().Add(grace)
	deadline := time.Now().Add(timeout)
	for {
		w.mu.Lock()
		inProgress := make([]string, 0, len(w.started))
		for _, name := range w.started {
			inProgress = append(inProgress, name)
		}
		readErr := w.readErr
		w.mu.Unlock()

		now := time.Now()
		switch {
		case len(inProgress) == 0 && !now.Before(graceEnds):
			return w.drain()
		case len(inProgress) > 0 && readErr != nil:
			return nil, fmt.Errorf("lost track of downloading %s: %w", strings.Join(inProgress, ", "), readErr)
		case len(inProgress) > 0 && !now.Before(deadline):
			return nil, fmt.Errorf("download of %s did not finish within %s", strings.Join(inProgress, ", "), timeout)
		}

		wake := deadline
		if len(inProgress) == 0 {
			wake = graceEnds
		}
		timer := time.NewTimer(time.Until(wake))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-w.changed:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// Close stops watching. Downloads still in progress are left to the browser.
func (w *DownloadWatcher) Close() error {
	w.cancel()
	err := w.conn.Close(websocket.StatusNormalClosure, "")
	<-w.done
	return err
}

func (w *DownloadWatcher) drain() ([]Download, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	completed, failed := w.completed, w.failed
	w.completed, w.failed = nil, nil
	if len(failed) > 0 {
		return completed, fmt.Errorf("download of %s was canceled", strings.Join(failed, ", "))
	}
	return completed, nil
}

func (w *DownloadWatcher) read(ctx context.Context, acknowledged chan<- error) {
	defer close(w.done)
	for {
		_, data, err := w.conn.Read(ctx)
		if err != nil {
			w.mu.Lock()
			w.readErr = err
			w.mu.Unlock()
			w.notify()
			select {
			case acknowledged <- err:
			default:
			}
			return
		}
		var message struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		switch {
		case message.ID != 0:
			var err error
			if message.Error != nil {
				err = errors.New("Browser.setDownloadBehavior: " + message.Error.Message)
			}
			select {
			case acknowledged <- err:
			default:
			}
		case message.Method == "Browser.downloadWillBegin":
			var event struct {
				GUID              string `json:"guid"`
				SuggestedFilename string `json:"suggestedFilename"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				w.mu.Lock()
				w.started[event.GUID] = event.SuggestedFilename
				w.mu.Unlock()
				w.notify()
			}
		case message.Method == "Browser.downloadProgress":
			var event struct {
				GUID  string `json:"guid"`
				State string `json:"state"`
			}
			if json.Unmarshal(message.Params, &event) == nil {
				w.finish(event.GUID, event.State)
			}
		}
	}
}

func (w *DownloadWatcher) finish(guid, state string) {
	if state != downloadCompleted && state != downloadCanceled {
		return
	}
	w.mu.Lock()
	name, ok := w.started[guid]
	delete(w.started, guid)
	if ok {
		if state == downloadCanceled {
			w.failed = append(w.failed, name)
		} else {
			w.completed = append(w.completed, w.save(guid, name))
		}
	}
	w.mu.Unlock()
	w.notify()
}

// save renames a finished download from its guid to a unique, safe version
// of the name the site suggested.
func (w *DownloadWatcher) save(guid, suggested string) Download {
	base := filepath.Base(filepath.Clean("/" + strings.ReplaceAll(suggested, `\`, "/")))
	if base == "" || base == "." || base == "/" {
		base = defaultDownloadName
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	name := base
	for i := 1; ; i++ {
		if _, err := os.Lstat(filepath.Join(w.dir, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = stem + "-" + strconv.Itoa(i) + ext
	}
	source := filepath.Join(w.dir, guid)
	target := filepath.Join(w.dir, name)
	if err := os.Rename(source, target); err != nil {
		// The file keeps its guid name when it cannot be renamed.
		return Download{Name: guid, Path: source}
	}
	return Download{Name: name, Path: target}
}

func (w *DownloadWatcher) notify() {
	select {
	case w.changed <- struct{}{}:
	default:
	}
}
