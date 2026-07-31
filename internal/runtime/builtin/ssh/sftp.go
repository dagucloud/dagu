// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ssh

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/pkg/sftp"
)

var _ executor.Executor = (*sftpExecutor)(nil)

type sftpExecutor struct {
	client      *Client
	direction   string // "upload" or "download"
	source      string
	destination string
	stdout      io.Writer
	stderr      io.Writer
}

// NewSFTPExecutor creates a new SFTP executor for file transfers.
func NewSFTPExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	client, err := resolveSSHClient(ctx, step)
	if err != nil {
		return nil, fmt.Errorf("failed to set up sftp step: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("ssh configuration is not found for sftp step")
	}

	config := step.ExecutorConfig.Config
	direction := getStringConfig(config, "direction", "upload")
	if direction != "upload" && direction != "download" {
		return nil, fmt.Errorf("invalid direction %q: must be 'upload' or 'download'", direction)
	}

	source := getStringConfig(config, "source", "")
	if source == "" {
		return nil, fmt.Errorf("source path is required for sftp step")
	}

	destination := getStringConfig(config, "destination", "")
	if destination == "" {
		return nil, fmt.Errorf("destination path is required for sftp step")
	}

	return &sftpExecutor{
		client:      client,
		direction:   direction,
		source:      source,
		destination: destination,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}, nil
}

// resolveSSHClient resolves the SSH client from step config or context.
func resolveSSHClient(ctx context.Context, step core.Step) (*Client, error) {
	if len(step.ExecutorConfig.Config) > 0 {
		return FromMapConfig(ctx, step.ExecutorConfig.Config)
	}
	return getSSHClientFromContext(ctx), nil
}

// getStringConfig returns a string value from config map with a default.
func getStringConfig(config map[string]any, key, defaultVal string) string {
	if val, ok := config[key].(string); ok && val != "" {
		return val
	}
	return defaultVal
}

func (e *sftpExecutor) SetStdout(out io.Writer) {
	e.stdout = out
}

func (e *sftpExecutor) SetStderr(out io.Writer) {
	e.stderr = out
}

func (e *sftpExecutor) Kill(_ os.Signal) error {
	// SFTP operations are not interruptible in the same way as SSH commands.
	// The operation will complete or fail on its own.
	return nil
}

func (e *sftpExecutor) Run(ctx context.Context) error {
	// Dial SSH connection directly - SFTP doesn't need a session, just the connection
	conn, err := e.client.dial()
	if err != nil {
		return fmt.Errorf("failed to connect to SSH server: %w", err)
	}
	defer conn.Close()

	sftpClient, err := sftp.NewClient(conn)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer sftpClient.Close()

	if e.direction == "download" {
		return e.download(ctx, sftpClient)
	}
	return e.upload(ctx, sftpClient)
}

// upload transfers files from local to remote.
func (e *sftpExecutor) upload(ctx context.Context, sftpClient *sftp.Client) error {
	// Check if source is a file or directory
	info, err := os.Stat(e.source)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", e.source, err)
	}

	if info.IsDir() {
		return e.uploadDir(ctx, sftpClient, e.source, e.destination)
	}
	return e.uploadFile(ctx, sftpClient, e.source, e.destination)
}

// uploadFile uploads a single file atomically.
// It writes to a temp file first, then renames to the final destination.
// This prevents partial files from being left on the remote server if the upload fails.
func (e *sftpExecutor) uploadFile(ctx context.Context, sftpClient *sftp.Client, localPath, remotePath string) error {
	logger.Info(ctx, "Uploading file",
		slog.String("source", localPath),
		slog.String("destination", remotePath),
	)

	// Open local file
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer localFile.Close()

	// Get file info for permissions
	info, err := localFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat local file: %w", err)
	}

	// Ensure remote directory exists (use path.Dir for POSIX remote paths)
	remoteDir := path.Dir(remotePath)
	if err := sftpClient.MkdirAll(remoteDir); err != nil {
		return fmt.Errorf("failed to create remote directory %s: %w", remoteDir, err)
	}

	// Use atomic upload: write to temp file, then rename
	// Random suffix prevents collisions and makes orphaned files identifiable
	var randBytes [8]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return fmt.Errorf("failed to generate random suffix for temp file: %w", err)
	}
	tempPath := remotePath + ".dagu-tmp-" + hex.EncodeToString(randBytes[:])

	// Create temp file on remote
	remoteFile, err := sftpClient.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create remote temp file: %w", err)
	}

	// Copy contents to temp file
	bytes, copyErr := io.Copy(remoteFile, localFile)

	// Close the file before rename (required on some systems)
	closeErr := remoteFile.Close()

	// If copy or close failed, clean up temp file and return error
	if copyErr != nil {
		_ = sftpClient.Remove(tempPath) // Best effort cleanup
		return fmt.Errorf("failed to copy file: %w", copyErr)
	}
	if closeErr != nil {
		_ = sftpClient.Remove(tempPath) // Best effort cleanup
		return fmt.Errorf("failed to close remote temp file: %w", closeErr)
	}

	// Set permissions on temp file before rename
	if err := sftpClient.Chmod(tempPath, info.Mode()); err != nil {
		logger.Warn(ctx, "Failed to set remote file permissions", tag.Error(err))
	}

	// Atomic rename: temp file -> final destination
	// Remove existing file first (rename won't overwrite on all systems)
	_ = sftpClient.Remove(remotePath)
	if err := sftpClient.Rename(tempPath, remotePath); err != nil {
		_ = sftpClient.Remove(tempPath) // Best effort cleanup
		return fmt.Errorf("failed to rename temp file to final destination: %w", err)
	}

	fmt.Fprintf(e.stdout, "Uploaded %s (%d bytes) to %s\n", localPath, bytes, remotePath)
	return nil
}

// uploadDir uploads a directory recursively.
func (e *sftpExecutor) uploadDir(ctx context.Context, sftpClient *sftp.Client, localDir, remoteDir string) error {
	logger.Info(ctx, "Uploading directory",
		slog.String("source", localDir),
		slog.String("destination", remoteDir),
	)

	return filepath.Walk(localDir, func(localPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from local directory
		relPath, err := filepath.Rel(localDir, localPath)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}
		// Use path.Join for remote POSIX paths (converts OS-specific separators to forward slashes)
		remotePath := path.Join(remoteDir, filepath.ToSlash(relPath))

		if info.IsDir() {
			// Create remote directory
			if err := sftpClient.MkdirAll(remotePath); err != nil {
				return fmt.Errorf("failed to create remote directory %s: %w", remotePath, err)
			}
			return nil
		}

		// Upload file
		return e.uploadFile(ctx, sftpClient, localPath, remotePath)
	})
}

// download transfers files from remote to local.
func (e *sftpExecutor) download(ctx context.Context, sftpClient *sftp.Client) error {
	// Check if source is a file or directory
	info, err := sftpClient.Stat(e.source)
	if err != nil {
		return fmt.Errorf("failed to stat remote %s: %w", e.source, err)
	}

	if info.IsDir() {
		return e.downloadDir(ctx, sftpClient, e.source, e.destination)
	}
	return e.downloadFile(ctx, sftpClient, e.source, e.destination)
}

// downloadFile downloads a single file.
func (e *sftpExecutor) downloadFile(ctx context.Context, sftpClient *sftp.Client, remotePath, localPath string) error {
	logger.Info(ctx, "Downloading file",
		slog.String("source", remotePath),
		slog.String("destination", localPath),
	)

	// Open remote file
	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer remoteFile.Close()

	// Get file info for permissions
	info, err := remoteFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat remote file: %w", err)
	}

	// Ensure local directory exists
	localDir := filepath.Dir(localPath)
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("failed to create local directory %s: %w", localDir, err)
	}

	// Create local file
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer localFile.Close()

	// Copy contents
	bytes, err := io.Copy(localFile, remoteFile)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	// Set permissions
	if err := os.Chmod(localPath, info.Mode()); err != nil {
		logger.Warn(ctx, "Failed to set local file permissions", tag.Error(err))
	}

	fmt.Fprintf(e.stdout, "Downloaded %s (%d bytes) to %s\n", remotePath, bytes, localPath)
	return nil
}

// downloadDir downloads a directory recursively.
func (e *sftpExecutor) downloadDir(ctx context.Context, sftpClient *sftp.Client, remoteDir, localDir string) error {
	logger.Info(ctx, "Downloading directory",
		slog.String("source", remoteDir),
		slog.String("destination", localDir),
	)

	walker := sftpClient.Walk(remoteDir)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			return fmt.Errorf("error walking remote directory: %w", err)
		}

		remotePath := walker.Path()
		info := walker.Stat()

		// Calculate relative path using POSIX path logic for remote paths
		// (path package doesn't have Rel, so use string manipulation)
		relPath := strings.TrimPrefix(strings.TrimPrefix(remotePath, remoteDir), "/")
		localPath := filepath.Join(localDir, filepath.FromSlash(relPath))

		if info.IsDir() {
			// Create local directory
			if err := os.MkdirAll(localPath, info.Mode()); err != nil {
				return fmt.Errorf("failed to create local directory %s: %w", localPath, err)
			}
			continue
		}

		// Download file
		if err := e.downloadFile(ctx, sftpClient, remotePath, localPath); err != nil {
			return err
		}
	}

	return nil
}

func init() {
	caps := core.ExecutorCapabilities{
		// SFTP executor doesn't use command/script - it uses source/destination paths
		Command:          false,
		MultipleCommands: false,
		Script:           false,
		Shell:            false,
	}
	executor.RegisterExecutor("sftp", NewSFTPExecutor, nil, caps)
}
