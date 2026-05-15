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
	"strings"
	"time"

	aquachecksum "github.com/aquaproj/aqua/v2/pkg/checksum"
	aquaparam "github.com/aquaproj/aqua/v2/pkg/config"
	aquareader "github.com/aquaproj/aqua/v2/pkg/config-reader"
	aquaconfig "github.com/aquaproj/aqua/v2/pkg/config/aqua"
	aquaregistryconfig "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquacontroller "github.com/aquaproj/aqua/v2/pkg/controller"
	aquadownload "github.com/aquaproj/aqua/v2/pkg/download"
	aquagithub "github.com/aquaproj/aqua/v2/pkg/github"
	aquaregistry "github.com/aquaproj/aqua/v2/pkg/install-registry"
	aquaruntime "github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/tools"
	"github.com/spf13/afero"
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

	commandSets, err := i.packageCommands(ctx, cfg, param, paths, rt)
	if err != nil {
		return nil, err
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
	for pkgIndex, pkg := range cfg.Packages {
		for _, command := range commandSets[pkgIndex] {
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

func (i *Installer) packageCommands(ctx context.Context, cfg *core.ToolConfig, param *aquaparam.Param, paths tools.CacheLayout, rt *aquaruntime.Runtime) ([][]string, error) {
	commandSets := make([][]string, len(cfg.Packages))
	needsInference := false
	for idx, pkg := range cfg.Packages {
		if len(pkg.Commands) == 0 {
			needsInference = true
			continue
		}
		commands := make([]string, 0, len(pkg.Commands))
		for _, command := range pkg.Commands {
			command = strings.TrimSpace(command)
			if !isCommandName(command) {
				return nil, fmt.Errorf("commands for %s@%s must be executable names, got %q", pkg.Package, pkg.Version, command)
			}
			commands = append(commands, command)
		}
		commandSets[idx] = commands
	}
	if !needsInference {
		return commandSets, nil
	}

	aquaCfg, err := i.readRenderedConfig(param, paths.ConfigFile)
	if err != nil {
		return nil, err
	}
	if len(aquaCfg.Packages) != len(cfg.Packages) {
		return nil, fmt.Errorf("infer aqua commands: rendered package count mismatch")
	}

	fs := afero.NewOsFs()
	checksums, updateChecksum, err := aquachecksum.Open(i.logger, fs, paths.ConfigFile, param.ChecksumEnabled(aquaCfg))
	if err != nil {
		return nil, fmt.Errorf("read aqua checksum file: %w", err)
	}
	defer updateChecksum()

	httpDownloader := aquadownload.NewHTTPDownloader(i.logger, i.httpClient)
	registryDownloader := aquadownload.NewGitHubContentFileDownloader(aquagithub.New(ctx, i.logger), httpDownloader)
	registryInstaller := aquaregistry.New(param, registryDownloader, fs, rt, nil, nil)
	registryContents := make(map[string]*aquaregistryconfig.Config, len(aquaCfg.Registries))

	for idx, pkg := range cfg.Packages {
		if len(commandSets[idx]) != 0 {
			continue
		}
		aquaPkg := aquaCfg.Packages[idx]
		registryContent, err := i.registryContent(ctx, registryInstaller, registryContents, aquaCfg, aquaPkg, paths.ConfigFile, checksums)
		if err != nil {
			return nil, err
		}
		pkgInfo := registryContent.Package(i.logger, aquaPkg.Name)
		if pkgInfo == nil {
			return nil, fmt.Errorf("infer aqua commands for %s@%s: package is not found in registry %q", pkg.Package, pkg.Version, aquaPkg.Registry)
		}
		commands, err := inferPackageCommands(i.logger, aquaPkg, pkgInfo, rt)
		if err != nil {
			return nil, fmt.Errorf("infer aqua commands for %s@%s: %w", pkg.Package, pkg.Version, err)
		}
		commandSets[idx] = commands
	}
	return commandSets, nil
}

func (i *Installer) readRenderedConfig(param *aquaparam.Param, configFile string) (*aquaconfig.Config, error) {
	cfg := &aquaconfig.Config{}
	reader := aquareader.New(afero.NewOsFs(), param)
	if err := reader.Read(i.logger, configFile, cfg); err != nil {
		return nil, fmt.Errorf("read generated aqua config: %w", err)
	}
	return cfg, nil
}

func (i *Installer) registryContent(ctx context.Context, registryInstaller *aquaregistry.Installer, registryContents map[string]*aquaregistryconfig.Config, cfg *aquaconfig.Config, pkg *aquaconfig.Package, configFile string, checksums *aquachecksum.Checksums) (*aquaregistryconfig.Config, error) {
	if pkg.Registry == "" {
		return nil, fmt.Errorf("infer aqua commands for %s@%s: registry is required", pkg.Name, pkg.Version)
	}
	if content, ok := registryContents[pkg.Registry]; ok {
		return content, nil
	}
	registry, ok := cfg.Registries[pkg.Registry]
	if !ok {
		return nil, fmt.Errorf("infer aqua commands for %s@%s: registry %q is not found", pkg.Name, pkg.Version, pkg.Registry)
	}
	content, err := registryInstaller.InstallRegistry(ctx, i.logger, registry, configFile, checksums)
	if err != nil {
		return nil, fmt.Errorf("install aqua registry %q: %w", pkg.Registry, err)
	}
	registryContents[pkg.Registry] = content
	return content, nil
}

func inferPackageCommands(logger *slog.Logger, pkg *aquaconfig.Package, pkgInfo *aquaregistryconfig.PackageInfo, rt *aquaruntime.Runtime) ([]string, error) {
	pkgInfo, err := pkgInfo.Override(logger, pkg.Version, rt)
	if err != nil {
		return nil, fmt.Errorf("apply version/runtime overrides: %w", err)
	}
	supported, err := pkgInfo.CheckSupported(rt, rt.GOOS+"/"+rt.GOARCH)
	if err != nil {
		return nil, fmt.Errorf("check platform support: %w", err)
	}
	if !supported {
		return nil, fmt.Errorf("package is not supported on %s/%s", rt.GOOS, rt.GOARCH)
	}

	seen := map[string]struct{}{}
	commands := make([]string, 0, len(pkgInfo.GetFiles()))
	for _, file := range pkgInfo.GetFiles() {
		command := strings.TrimSpace(file.Name)
		if command == "" {
			return nil, fmt.Errorf("registry file entry has empty name; specify commands explicitly")
		}
		if !isCommandName(command) {
			return nil, fmt.Errorf("registry file name %q is not a safe executable name; specify commands explicitly", command)
		}
		if _, ok := seen[command]; ok {
			continue
		}
		seen[command] = struct{}{}
		commands = append(commands, command)
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("no executable files found; specify commands explicitly")
	}
	return commands, nil
}

func isCommandName(command string) bool {
	if command == "" {
		return false
	}
	for _, r := range command {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '+':
		default:
			return false
		}
	}
	return true
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
