// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
)

// Background service definitions written by scripts/installer.sh and
// scripts/installer.ps1.
const (
	launchdServiceLabel  = "local.dagu.server"
	systemdServiceUnit   = "dagu.service"
	systemdSystemUnitDir = "/etc/systemd/system"
	winswServiceConfig   = "dagu-service.xml"
	winswServiceName     = "Dagu"
)

// Commands that add a systemd drop-in limiting a stop of the service to the
// Dagu process itself, then reload systemd.
const (
	systemKeepRunsCommand = `sudo sh -c 'mkdir -p /etc/systemd/system/dagu.service.d && printf "[Service]\nKillMode=process\n" > /etc/systemd/system/dagu.service.d/keep-runs.conf && systemctl daemon-reload'`
	userKeepRunsCommand   = `mkdir -p "$HOME/.config/systemd/user/dagu.service.d" && printf '[Service]\nKillMode=process\n' > "$HOME/.config/systemd/user/dagu.service.d/keep-runs.conf" && systemctl --user daemon-reload`
)

// Service is the background service the Dagu installer set up on this machine.
type Service struct {
	// RestartCommand is the shell command that restarts the service.
	RestartCommand string
	// StopsRuns reports that restarting the service also ends runs in progress.
	StopsRuns bool
	// KeepRunsCommand is the shell command that reconfigures the service so a
	// restart leaves runs in progress running. It is empty when StopsRuns is
	// false or when the service manager offers no such setting.
	KeepRunsCommand string
	// RestartsOnExit reports that the service starts Dagu again after Dagu
	// exits by itself to load an upgraded binary.
	RestartsOnExit bool
}

// DetectService returns the background service the Dagu installer set up on
// this machine, or nil when none is found.
func DetectService() *Service {
	var execDir string
	if execPath, err := GetExecutablePath(); err == nil {
		execDir = filepath.Dir(execPath)
	}
	homeDir, _ := os.UserHomeDir()
	return detectService(runtime.GOOS, execDir, homeDir, systemdSystemUnitDir)
}

// RunsUnderService reports whether this process is the one the service of
// DetectService runs, so that the service starts it again when it exits.
// A Dagu started by hand on a machine with that service is not.
func RunsUnderService() bool {
	var execDir string
	if execPath, err := GetExecutablePath(); err == nil {
		execDir = filepath.Dir(execPath)
	}
	cgroup, _ := os.ReadFile("/proc/self/cgroup")
	return runsUnderService(runtime.GOOS, os.Getenv, string(cgroup), execDir)
}

func runsUnderService(goos string, getenv func(string) string, cgroup, execDir string) bool {
	switch goos {
	case "darwin":
		// launchd names the job of each process it starts.
		return getenv("XPC_SERVICE_NAME") == launchdServiceLabel
	case "linux":
		// systemd keeps the processes of a unit in a cgroup named after it.
		for line := range strings.SplitSeq(cgroup, "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), "/"+systemdServiceUnit) {
				return true
			}
		}
	case "windows":
		// WinSW names the service and the directory of its config.
		return strings.EqualFold(getenv("SERVICE_ID"), winswServiceName) &&
			execDir != "" && strings.EqualFold(filepath.Clean(getenv("BASE")), filepath.Clean(execDir))
	}
	return false
}

func detectService(goos, execDir, homeDir, systemUnitDir string) *Service {
	switch goos {
	case "darwin":
		// launchd ends only the job's process group, and each run starts its
		// own, so runs outlive a restart.
		if homeDir != "" && fileutil.IsFile(filepath.Join(homeDir, "Library", "LaunchAgents", launchdServiceLabel+".plist")) {
			return &Service{
				RestartCommand: "launchctl kickstart -k gui/$(id -u)/" + launchdServiceLabel,
				RestartsOnExit: true,
			}
		}
	case "linux":
		if unit := filepath.Join(systemUnitDir, systemdServiceUnit); fileutil.IsFile(unit) {
			return systemdService(unit, "sudo systemctl restart dagu", systemKeepRunsCommand)
		}
		if homeDir != "" {
			if unit := filepath.Join(homeDir, ".config", "systemd", "user", systemdServiceUnit); fileutil.IsFile(unit) {
				return systemdService(unit, "systemctl --user restart dagu", userKeepRunsCommand)
			}
		}
	case "windows":
		// The installer keeps the service wrapper next to the binary it runs.
		// WinSW stops the whole process tree of the service, runs included.
		if execDir != "" && fileutil.IsFile(filepath.Join(execDir, winswServiceConfig)) {
			return &Service{
				RestartCommand: "Restart-Service " + winswServiceName,
				StopsRuns:      true,
				RestartsOnExit: true,
			}
		}
	}
	return nil
}

// systemdService describes the service of the systemd unit at unitPath.
func systemdService(unitPath, restartCommand, keepRunsCommand string) *Service {
	service := &Service{RestartCommand: restartCommand}
	// The default KillMode ends every process of the unit, runs included.
	if killMode := systemdSetting(unitPath, "KillMode"); killMode != "process" && killMode != "none" {
		service.StopsRuns = true
		service.KeepRunsCommand = keepRunsCommand
	}
	// Dagu exits cleanly to load an upgraded binary, and the default Restart
	// starts nothing afterwards.
	if restart := systemdSetting(unitPath, "Restart"); restart == "always" || restart == "on-success" {
		service.RestartsOnExit = true
	}
	return service
}

// systemdSetting returns the value of key in the [Service] section of the
// unit at unitPath, or "" for its default. systemd applies the drop-ins next
// to the unit in file name order.
func systemdSetting(unitPath, key string) string {
	files := []string{unitPath}
	dropInDir := unitPath + ".d"
	if entries, err := os.ReadDir(dropInDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".conf") {
				files = append(files, filepath.Join(dropInDir, entry.Name()))
			}
		}
	}

	var setting string
	for _, file := range files {
		if value, ok := readServiceSetting(file, key); ok {
			setting = value
		}
	}
	return setting
}

// readServiceSetting returns the last assignment of key in the [Service]
// section of a unit file. An empty value resets the setting to its default.
func readServiceSetting(path, key string) (string, bool) {
	f, err := os.Open(path) //nolint:gosec // path names a systemd unit file of the Dagu service
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	var (
		setting string
		found   bool
		section string
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "["):
			section = line
		case section == "[Service]":
			name, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(name) == key {
				setting, found = strings.TrimSpace(value), true
			}
		}
	}
	return setting, found
}

// Restart restarts the service. The command shares the caller's terminal, so
// sudo can ask for a password.
func (s *Service) Restart(ctx context.Context) error {
	return runShell(ctx, s.RestartCommand)
}

// KeepRuns runs KeepRunsCommand. The command shares the caller's terminal, so
// sudo can ask for a password.
func (s *Service) KeepRuns(ctx context.Context) error {
	if s.KeepRunsCommand == "" {
		return fmt.Errorf("the service offers no setting that keeps runs across restarts")
	}
	return runShell(ctx, s.KeepRunsCommand)
}

func runShell(ctx context.Context, command string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command) //nolint:gosec // command is a fixed service command
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", command) //nolint:gosec // command is a fixed service command
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	return nil
}
