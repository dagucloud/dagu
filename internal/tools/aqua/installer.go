// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	aquaparam "github.com/aquaproj/aqua/v2/pkg/config"
	aquacontroller "github.com/aquaproj/aqua/v2/pkg/controller"
	aquaruntime "github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/tools"
)

const (
	defaultMaxParallelism = 5
	lockStaleThreshold    = 10 * time.Minute
	lockRetryInterval     = 100 * time.Millisecond
	lockHeartbeatEvery    = 30 * time.Second
)

// Installer installs aqua-backed Dagu tools.
type Installer struct {
	logger     *slog.Logger
	httpClient *http.Client
}

// Option configures an Installer.
type Option func(*Installer)

// WithLogger configures the logger passed to aqua.
func WithLogger(logger *slog.Logger) Option {
	return func(installer *Installer) {
		installer.logger = logger
	}
}

// WithHTTPClient configures the HTTP client passed to aqua.
func WithHTTPClient(client *http.Client) Option {
	return func(installer *Installer) {
		installer.httpClient = client
	}
}

// New returns an aqua-backed Dagu tool installer.
func New(opts ...Option) *Installer {
	installer := &Installer{
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		httpClient: http.DefaultClient,
	}
	for _, opt := range opts {
		opt(installer)
	}
	return installer
}

// Install installs declared tools and returns resolved command paths.
func (i *Installer) Install(ctx context.Context, cfg *core.ToolConfig, opts tools.InstallOptions) (*tools.Manifest, error) {
	if cfg == nil {
		return nil, fmt.Errorf("tools config is required")
	}
	if cfg.Provider != "" && cfg.Provider != providerAqua {
		return nil, fmt.Errorf("unsupported tools provider %q", cfg.Provider)
	}
	cfg = effectiveToolConfig(cfg)

	rt := aquaruntime.NewR(ctx)
	platform := opts.Platform
	if platform == "" {
		platform = rt.Env()
	}
	hash, err := tools.ToolsetHash(cfg, platform)
	if err != nil {
		return nil, err
	}
	paths, err := tools.CachePaths(opts.DataDir, platform, hash)
	if err != nil {
		return nil, err
	}
	unlock, err := i.lockToolRoot(ctx, paths.RootDir)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// Tool caches live under the worker-local data dir and are owned by the
	// worker process user; group-readable directories are enough for shared
	// process access without making downloaded binaries world-readable.
	if err := os.MkdirAll(paths.EnvDir, 0o750); err != nil {
		return nil, fmt.Errorf("create aqua env dir: %w", err)
	}
	if err := os.MkdirAll(paths.RootDir, 0o750); err != nil {
		return nil, fmt.Errorf("create aqua root dir: %w", err)
	}
	data, err := RenderConfigForPlatform(cfg, platform)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(paths.ConfigFile, data, 0o600); err != nil {
		return nil, fmt.Errorf("write generated aqua config: %w", err)
	}

	param := aquaParam(paths, opts.WorkDir)
	updateChecksumController, err := aquacontroller.InitializeUpdateChecksumCommandController(ctx, i.logger, param, i.httpClient, rt)
	if err != nil {
		return nil, fmt.Errorf("initialize aqua update-checksum controller: %w", err)
	}
	if err := updateChecksumController.UpdateChecksum(ctx, i.logger, param); err != nil {
		return nil, fmt.Errorf("update aqua checksums: %w", err)
	}

	installController, err := aquacontroller.InitializeInstallCommandController(ctx, i.logger, param, i.httpClient, rt)
	if err != nil {
		return nil, fmt.Errorf("initialize aqua install controller: %w", err)
	}
	if err := installController.Install(ctx, i.logger, param); err != nil {
		return nil, fmt.Errorf("install aqua tools: %w", err)
	}

	whichController := aquacontroller.InitializeWhichCommandController(ctx, i.logger, param, i.httpClient, rt)
	manifest := &tools.Manifest{
		Provider:     providerAqua,
		Platform:     platform,
		Hash:         hash,
		RootDir:      paths.RootDir,
		EnvDir:       paths.EnvDir,
		BinDir:       paths.BinDir,
		Config:       paths.ConfigFile,
		Checksum:     paths.ChecksumFile,
		ManifestFile: paths.ManifestFile,
		Commands:     make(map[string]tools.Command),
	}
	for _, pkg := range cfg.Packages {
		for _, command := range pkg.Commands {
			if existing, ok := manifest.Commands[command]; ok {
				return nil, fmt.Errorf(
					"duplicate command %q declared by %s@%s and %s@%s",
					command,
					existing.Package,
					existing.Version,
					pkg.Package,
					pkg.Version,
				)
			}
			resolved, err := whichController.Which(ctx, i.logger, param, command)
			if err != nil {
				return nil, fmt.Errorf("resolve aqua command %q: %w", command, err)
			}
			if resolved.Package == nil {
				return nil, fmt.Errorf("resolve aqua command %q: resolved from ambient PATH, not declared tools", command)
			}
			if filepath.Clean(resolved.ConfigFilePath) != filepath.Clean(paths.ConfigFile) {
				return nil, fmt.Errorf("resolve aqua command %q: resolved from unexpected config %q", command, resolved.ConfigFilePath)
			}
			if resolved.Package.Package == nil || resolved.Package.Package.Name != pkg.Package || resolved.Package.Package.Version != pkg.Version {
				return nil, fmt.Errorf("resolve aqua command %q: resolved package does not match declaration", command)
			}
			shimPath, err := createCommandShim(paths.BinDir, command, resolved.ExePath, platform)
			if err != nil {
				return nil, err
			}
			manifest.Commands[command] = tools.Command{
				Name:    command,
				Path:    shimPath,
				Package: pkg.Package,
				Version: pkg.Version,
			}
		}
	}
	if err := writeManifest(paths.ManifestFile, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (i *Installer) lockToolRoot(ctx context.Context, rootDir string) (func(), error) {
	lock := dirlock.New(rootDir, &dirlock.LockOptions{
		StaleThreshold: lockStaleThreshold,
		RetryInterval:  lockRetryInterval,
	})
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("lock aqua tool root: %w", err)
	}

	heartbeatCtx, stopHeartbeat := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(lockHeartbeatEvery)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := lock.Heartbeat(context.Background()); err != nil {
					i.logger.Debug("heartbeat aqua tool root lock", "err", err)
				}
			}
		}
	}()

	return func() {
		stopHeartbeat()
		<-done
		if err := lock.Unlock(); err != nil {
			i.logger.Debug("unlock aqua tool root", "err", err)
		}
	}, nil
}

func aquaParam(paths tools.CacheLayout, workDir string) *aquaparam.Param {
	cwd := workDir
	if cwd == "" {
		cwd = paths.EnvDir
	}
	return &aquaparam.Param{
		ConfigFilePath:         paths.ConfigFile,
		RootDir:                paths.RootDir,
		CWD:                    cwd,
		MaxParallelism:         defaultMaxParallelism,
		DisableLazyInstall:     true,
		ProgressBar:            false,
		Prune:                  true,
		Checksum:               true,
		RequireChecksum:        true,
		EnforceChecksum:        true,
		EnforceRequireChecksum: true,
	}
}
