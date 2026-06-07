package dagutest

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type Runner struct {
	t   *testing.T
	dir string
}

type Result struct {
	t        *testing.T
	exitCode int
	stdout   string
	stderr   string
}

func New(t *testing.T, project string) *Runner {
	t.Helper()

	r := &Runner{
		t:   t,
		dir: t.TempDir(),
	}
	src := filepath.Join("testdata", filepath.FromSlash(project))
	if err := os.CopyFS(r.dir, os.DirFS(src)); err != nil {
		r.t.Fatalf("copying project %s: %v", project, err)
	}
	return r
}

func (r *Runner) Run(args ...string) *Result {
	r.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	// Binary-level tests intentionally execute the configured Dagu binary.
	cmd := exec.CommandContext(ctx, daguBinary(r.t), args...) //nolint:gosec
	cmd.Dir = r.dir
	cmd.Env = append(isolatedEnv(r.t), "PWD="+r.dir)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		r.t.Fatalf("dagu command timed out: dagu %s", strings.Join(args, " "))
	}

	exitCode := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			r.t.Fatalf("running dagu %s: %v", strings.Join(args, " "), err)
		}
		exitCode = exitErr.ExitCode()
	}

	return &Result{
		t:        r.t,
		exitCode: exitCode,
		stdout:   stdout.String(),
		stderr:   stderr.String(),
	}
}

func (r *Runner) ExpectNoFile(name string) {
	r.t.Helper()

	path := filepath.Join(r.dir, filepath.FromSlash(name))
	if _, err := os.Stat(path); err == nil {
		r.t.Fatalf("expected %s to be absent", name)
	} else if !os.IsNotExist(err) {
		r.t.Fatalf("checking %s: %v", name, err)
	}
}

func (r *Result) ExpectExitCode(code int) {
	r.t.Helper()

	if r.exitCode != code {
		r.t.Fatalf("expected exit code %d, got %d\nstdout:\n%s\nstderr:\n%s",
			code, r.exitCode, r.stdout, r.stderr)
	}
}

func (r *Result) ExpectStdout(stdout string) {
	r.t.Helper()

	if r.stdout != stdout {
		r.t.Fatalf("expected stdout %q, got:\n%s", stdout, r.stdout)
	}
}

func (r *Result) ExpectStderrContains(parts ...string) {
	r.t.Helper()

	for _, part := range parts {
		if !strings.Contains(r.stderr, part) {
			r.t.Fatalf("expected stderr to contain %q, got:\n%s", part, r.stderr)
		}
	}
}

func daguBinary(t *testing.T) string {
	t.Helper()

	bin := os.Getenv("DAGU_BIN")
	if bin == "" {
		t.Skip("set DAGU_BIN to run binary-level blackbox tests")
	}

	if filepath.IsAbs(bin) {
		return statBinary(t, bin)
	}

	if !hasPathSeparator(bin) {
		path, err := exec.LookPath(bin)
		if err != nil {
			t.Fatalf("resolving DAGU_BIN %q: %v", bin, err)
		}
		return path
	}

	return statBinary(t, filepath.Join("..", bin))
}

func hasPathSeparator(path string) bool {
	return strings.Contains(path, "/") || strings.Contains(path, `\`)
}

func statBinary(t *testing.T, path string) string {
	t.Helper()

	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolving DAGU_BIN %q: %v", path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("checking DAGU_BIN %q: %v", abs, err)
	}
	return abs
}

func isolatedEnv(t *testing.T) []string {
	t.Helper()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(root, "xdg")
	if err := os.MkdirAll(config, 0o750); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}

	env := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "DAGU_") || isolatedEnvKey(key) {
			continue
		}
		env = append(env, entry)
	}

	env = append(env,
		"CI=1",
		"NO_COLOR=1",
		"DAGU_HOME="+filepath.Join(root, "dagu"),
		"HOME="+home,
		"XDG_CONFIG_HOME="+config,
		"APPDATA="+filepath.Join(root, "appdata"),
		"USERPROFILE="+home,
	)

	return env
}

func isolatedEnvKey(key string) bool {
	switch key {
	case "APPDATA", "HOME", "PWD", "USERPROFILE", "XDG_CONFIG_HOME":
		return true
	default:
		return false
	}
}
