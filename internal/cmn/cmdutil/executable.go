// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmdutil

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LookupEnv returns the value of the first "key=value" entry in envs whose
// key matches key. The key comparison is case-insensitive on Windows.
func LookupEnv(envs []string, key string) (string, bool) {
	for _, entry := range envs {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if runtime.GOOS == "windows" {
			if strings.EqualFold(name, key) {
				return value, true
			}
			continue
		}
		if name == key {
			return value, true
		}
	}
	return "", false
}

// isExecutableFile reports whether path names an existing regular file with
// executable permission. On Windows any existing regular file counts.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode()&0o111 != 0
}

// IsExecutableFileInEnv reports whether path resolves to a file the process
// starter can launch under envs. Off Windows the file must exist and carry an
// executable bit. On Windows it follows the os/exec lookup rules: a name with
// an extension is tried as is, then each PATHEXT suffix is appended, and the
// first existing candidate must carry a PATHEXT extension — a match like
// tool.txt resolves at lookup but cannot launch. An extensionless name never
// matches as is, so tool resolves tool.cmd even when a file named tool exists.
func IsExecutableFileInEnv(path string, envs []string) bool {
	if runtime.GOOS != "windows" {
		return isExecutableFile(path)
	}
	pathextEnv, _ := LookupEnv(envs, "PATHEXT")
	for _, candidate := range pathCandidates(path, pathextEnv) {
		if isExecutableFile(candidate) {
			return pathextContains(pathextEnv, filepath.Ext(candidate))
		}
	}
	return false
}

// LookPathInEnv resolves name to an executable file using the PATH entry in
// envs, honoring PATHEXT on Windows. When envs carries no PATH entry it falls
// back to FindExecutable. Names containing a path separator are checked as
// file paths.
func LookPathInEnv(name string, envs []string) (string, error) {
	return LookPathInEnvDir(name, envs, "")
}

// LookPathInEnvDir is LookPathInEnv with relative names resolved against dir,
// matching how a process started in dir resolves them: relative PATH entries
// and relative path-form names anchor to dir instead of the process working
// directory. An empty dir keeps process working directory semantics.
func LookPathInEnvDir(name string, envs []string, dir string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		path := name
		if dir != "" && !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if IsExecutableFileInEnv(path, envs) {
			return path, nil
		}
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	if pathEnv, ok := LookupEnv(envs, "PATH"); ok {
		pathextEnv, _ := LookupEnv(envs, "PATHEXT")
		return lookPathInPATH(name, pathEnv, pathextEnv, dir)
	}
	if resolved, ok := FindExecutable(name); ok {
		return resolved, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func lookPathInPATH(command, pathEnv, pathextEnv, baseDir string) (string, error) {
	var lastErr error
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		if baseDir != "" && !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		for _, candidate := range pathCandidates(filepath.Join(dir, command), pathextEnv) {
			if isExecutableFile(candidate) {
				if runtime.GOOS == "windows" && !pathextContains(pathextEnv, filepath.Ext(candidate)) {
					// The starter resolves this candidate but cannot launch
					// it; later entries never get a chance.
					return "", &exec.Error{Name: command, Err: exec.ErrNotFound}
				}
				return candidate, nil
			}
			if _, err := os.Stat(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
				lastErr = err
			}
		}
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", &exec.Error{Name: command, Err: exec.ErrNotFound}
}

func pathCandidates(candidate, pathextEnv string) []string {
	if runtime.GOOS != "windows" {
		return []string{candidate}
	}

	exts := pathextList(pathextEnv)
	candidates := make([]string, 0, len(exts)+1)
	// A name with an extension resolves as is first; every name then tries
	// each PATHEXT suffix (e.g. tool.v2 resolves tool.v2.exe). Like os/exec,
	// an extensionless name is skipped as is: npm installs a POSIX shim named
	// tool next to tool.cmd.
	if filepath.Ext(candidate) != "" {
		candidates = append(candidates, candidate)
	}
	for _, ext := range exts {
		candidates = append(candidates, candidate+ext)
	}
	return candidates
}

// pathextList splits a PATHEXT value into normalized extensions, falling
// back to the Windows default when empty.
func pathextList(pathextEnv string) []string {
	pathext := pathextEnv
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	var exts []string
	for ext := range strings.SplitSeq(pathext, ";") {
		if ext == "" {
			continue
		}
		if ext[0] != '.' {
			ext = "." + ext
		}
		exts = append(exts, ext)
	}
	return exts
}

func pathextContains(pathextEnv, ext string) bool {
	if ext == "" {
		return false
	}
	for _, e := range pathextList(pathextEnv) {
		if strings.EqualFold(e, ext) {
			return true
		}
	}
	return false
}

// FindExecutable resolves cmd from PATH first, then falls back to common
// Windows compatibility locations for Git-provided Unix tooling.
func FindExecutable(cmd string) (string, bool) {
	if cmd == "" {
		return "", false
	}
	if path, err := exec.LookPath(cmd); err == nil {
		return path, true
	}
	if runtime.GOOS != "windows" {
		return "", false
	}
	if path := findWindowsCompatExecutable(cmd); path != "" {
		return path, true
	}
	return "", false
}

// ResolveExecutable returns the best-effort resolved executable path for cmd.
// If no compatibility path is found, the original value is returned unchanged.
func ResolveExecutable(cmd string) string {
	if runtime.GOOS != "windows" {
		return cmd
	}
	if path, ok := FindExecutable(cmd); ok {
		return path
	}
	return cmd
}

func findWindowsCompatExecutable(cmd string) string {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(cmd, "\\", "/")))
	name = strings.TrimSuffix(name, ".exe")

	var candidates []string
	switch name {
	case "bash":
		candidates = windowsGitCandidates("bash.exe")
	case "sh":
		candidates = windowsGitCandidates("sh.exe")
	case "env":
		candidates = windowsGitCandidates("env.exe")
	default:
		return ""
	}

	for _, candidate := range candidates {
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate
		}
	}
	return ""
}

func windowsGitCandidates(exe string) []string {
	var roots []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if value := strings.TrimSpace(os.Getenv(env)); value != "" {
			roots = append(roots, value)
		}
	}

	var candidates []string
	for _, root := range roots {
		switch filepath.Base(root) {
		case "Programs":
			candidates = append(candidates,
				filepath.Join(root, "Git", "bin", exe),
				filepath.Join(root, "Git", "usr", "bin", exe),
			)
		default:
			candidates = append(candidates,
				filepath.Join(root, "Git", "bin", exe),
				filepath.Join(root, "Git", "usr", "bin", exe),
				filepath.Join(root, "Programs", "Git", "bin", exe),
				filepath.Join(root, "Programs", "Git", "usr", "bin", exe),
			)
		}
	}

	return candidates
}
