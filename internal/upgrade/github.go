// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/go-resty/resty/v2"
)

const (
	githubAPIURL = "https://api.github.com/repos/dagucloud/dagu/releases"

	defaultTimeout = 30 * time.Second
)

// Release represents a GitHub release.
type Release struct {
	TagName    string  `json:"tag_name"`
	Name       string  `json:"name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	HTMLURL    string  `json:"html_url"`
	Assets     []Asset `json:"assets"`
}

// Asset represents a GitHub release asset.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

// GitHubClient is an HTTP client for the GitHub Releases API.
type GitHubClient struct {
	client *resty.Client
}

// NewGitHubClient creates a new GitHub API client.
func NewGitHubClient() *GitHubClient {
	client := resty.New().
		SetTimeout(defaultTimeout).
		SetHeader("Accept", "application/vnd.github+json").
		SetHeader("User-Agent", "dagu-upgrade-client")
	return &GitHubClient{client: client}
}

// GetLatestRelease fetches the latest release from GitHub.
// If includePreRelease is true, it may return a pre-release version.
func (c *GitHubClient) GetLatestRelease(ctx context.Context, includePreRelease bool) (*Release, error) {
	policy := newUpgradeRetryPolicy()

	if !includePreRelease {
		var release Release
		err := backoff.Retry(ctx, func(ctx context.Context) error {
			resp, err := c.client.R().SetContext(ctx).SetResult(&release).
				Get(githubAPIURL + "/latest")
			if err != nil {
				return err
			}
			return classifyResponse(resp)
		}, policy, isRetriableError)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch latest release: %w", err)
		}
		return &release, nil
	}

	var releases []Release
	err := backoff.Retry(ctx, func(ctx context.Context) error {
		resp, err := c.client.R().SetContext(ctx).SetResult(&releases).
			SetQueryParam("per_page", "10").Get(githubAPIURL)
		if err != nil {
			return err
		}
		return classifyResponse(resp)
	}, policy, isRetriableError)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch releases: %w", err)
	}

	for i := range releases {
		if !releases[i].Draft {
			return &releases[i], nil
		}
	}

	return nil, fmt.Errorf("no releases found")
}

// GetRelease fetches a specific release by tag.
func (c *GitHubClient) GetRelease(ctx context.Context, tag string) (*Release, error) {
	tag = NormalizeVersionTag(tag)
	if err := ValidateVersionTag(tag); err != nil {
		return nil, err
	}
	policy := newUpgradeRetryPolicy()

	var release Release
	err := backoff.Retry(ctx, func(ctx context.Context) error {
		resp, err := c.client.R().SetContext(ctx).SetResult(&release).
			Get(fmt.Sprintf("%s/tags/%s", githubAPIURL, url.PathEscape(tag)))
		if err != nil {
			return err
		}
		if resp.StatusCode() == 404 {
			return &httpError{statusCode: 404, message: fmt.Sprintf("release %s not found", tag)}
		}
		return classifyResponse(resp)
	}, policy, isRetriableError)
	if err != nil {
		return nil, err
	}
	return &release, nil
}

// GetChecksums downloads and parses checksums.txt from a release.
// Returns a map of filename to SHA256 hash.
func (c *GitHubClient) GetChecksums(ctx context.Context, release *Release) (map[string]string, error) {
	var checksumsURL string
	for _, asset := range release.Assets {
		if asset.Name == "checksums.txt" {
			checksumsURL = asset.BrowserDownloadURL
			break
		}
	}

	if checksumsURL == "" {
		return nil, fmt.Errorf("checksums.txt not found in release %s", release.TagName)
	}

	policy := newUpgradeRetryPolicy()
	var body string
	err := backoff.Retry(ctx, func(ctx context.Context) error {
		resp, err := c.client.R().SetContext(ctx).Get(checksumsURL)
		if err != nil {
			return err
		}
		if statusErr := classifyResponse(resp); statusErr != nil {
			return statusErr
		}
		body = resp.String()
		return nil
	}, policy, isRetriableError)
	if err != nil {
		return nil, fmt.Errorf("failed to download checksums.txt: %w", err)
	}
	return parseChecksums(body)
}

// parseChecksums parses the checksums.txt format (sha256sum output format).
func parseChecksums(content string) (map[string]string, error) {
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			parts = strings.Fields(line)
			if len(parts) != 2 {
				continue
			}
		}

		hash := strings.TrimSpace(parts[0])
		filename := strings.TrimSpace(parts[1])

		if hash != "" && filename != "" {
			checksums[filename] = hash
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error parsing checksums: %w", err)
	}

	if len(checksums) == 0 {
		return nil, fmt.Errorf("no valid checksums found")
	}

	return checksums, nil
}

// FindAsset finds the appropriate asset for the given platform in a release.
func FindAsset(release *Release, platform Platform, version string) (*Asset, error) {
	expectedName := platform.AssetName(version)

	for i := range release.Assets {
		if release.Assets[i].Name == expectedName {
			return &release.Assets[i], nil
		}
	}

	return nil, fmt.Errorf("asset %s not found in release %s", expectedName, release.TagName)
}
