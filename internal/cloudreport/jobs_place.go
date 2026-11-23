// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudreport

import (
	"net/url"
	"os"
	"strings"
)

const (
	// maxHostBytes is the longest hostname a report carries.
	maxHostBytes = 253
	// maxPlatformURLBytes is the longest link to a CI run a report carries.
	maxPlatformURLBytes = 2048
)

// platform is a system a job's machine can run in, told from the
// environment it gives the machine, with the link to the CI run when it has
// one.
type platform struct {
	name   string
	detect func(getenv func(string) string) bool
	link   func(getenv func(string) string) string
}

// platforms are checked in order: CI systems before Kubernetes, since many
// CI runners run on it.
var platforms = []platform{
	{
		name:   "github_actions",
		detect: equals("GITHUB_ACTIONS", "true"),
		link: func(getenv func(string) string) string {
			server, repo, run := getenv("GITHUB_SERVER_URL"), getenv("GITHUB_REPOSITORY"), getenv("GITHUB_RUN_ID")
			if server == "" || repo == "" || run == "" {
				return ""
			}
			link := strings.TrimRight(server, "/") + "/" + repo + "/actions/runs/" + run
			if attempt := getenv("GITHUB_RUN_ATTEMPT"); attempt != "" {
				link += "/attempts/" + attempt
			}
			return link
		},
	},
	{name: "gitlab_ci", detect: equals("GITLAB_CI", "true"), link: variable("CI_JOB_URL")},
	{name: "buildkite", detect: equals("BUILDKITE", "true"), link: variable("BUILDKITE_BUILD_URL")},
	{name: "circleci", detect: equals("CIRCLECI", "true"), link: variable("CIRCLE_BUILD_URL")},
	{
		name:   "azure_pipelines",
		detect: func(getenv func(string) string) bool { return strings.EqualFold(getenv("TF_BUILD"), "true") },
		link: func(getenv func(string) string) string {
			collection, project, build := getenv("SYSTEM_COLLECTIONURI"), getenv("SYSTEM_TEAMPROJECT"), getenv("BUILD_BUILDID")
			if collection == "" || project == "" || build == "" {
				return ""
			}
			return strings.TrimRight(collection, "/") + "/" + url.PathEscape(project) +
				"/_build/results?buildId=" + url.QueryEscape(build)
		},
	},
	{name: "jenkins", detect: set("JENKINS_URL"), link: variable("BUILD_URL")},
	{name: "cloud_run", detect: set("CLOUD_RUN_JOB")},
	{name: "aws_batch", detect: set("AWS_BATCH_JOB_ID")},
	{name: "kubernetes", detect: set("KUBERNETES_SERVICE_HOST")},
}

func equals(key, value string) func(func(string) string) bool {
	return func(getenv func(string) string) bool { return getenv(key) == value }
}

func set(key string) func(func(string) string) bool {
	return func(getenv func(string) string) bool { return getenv(key) != "" }
}

func variable(key string) func(func(string) string) string {
	return func(getenv func(string) string) string { return getenv(key) }
}

// placeOf is where a job run runs: the first platform its environment
// matches, the link to its CI run when that is an https URL of at most
// maxPlatformURLBytes, and the machine's hostname. Nil functions read the
// process's own.
func placeOf(getenv func(string) string, hostname func() (string, error)) jobPlace {
	if getenv == nil {
		getenv = os.Getenv
	}
	if hostname == nil {
		hostname = os.Hostname
	}
	var place jobPlace
	for _, p := range platforms {
		if !p.detect(getenv) {
			continue
		}
		place.Platform = p.name
		if p.link != nil {
			place.PlatformURL = httpsLink(p.link(getenv))
		}
		break
	}
	if host, err := hostname(); err == nil {
		place.Host = truncateBytes(strings.TrimSpace(host), maxHostBytes)
	}
	return place
}

// httpsLink returns link when it is an https URL with a host, of at most
// maxPlatformURLBytes, and "" otherwise.
func httpsLink(link string) string {
	if link == "" || len(link) > maxPlatformURLBytes {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	// A user and password in the link would reach Dagu Console and show
	// there.
	u.User = nil
	return u.String()
}
