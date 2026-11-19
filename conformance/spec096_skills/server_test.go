// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec096_skills_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// instance is a running `dagu server` on a skills directory.
type instance struct {
	t    *testing.T
	base string
}

// startServer starts `dagu server` with its own home and the shared DAGs
// directory. Two instances with the same directory stand for two servers of one
// HA group.
func startServer(t *testing.T, dagu *harness.Runner, dags string) *instance {
	t.Helper()
	port := harness.FreePort(t)
	home := filepath.Join(t.TempDir(), "dagu")
	proc := dagu.StartWithEnv([]string{"DAGU_HOME=" + home},
		"server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port), "--dags="+dags)
	s := &instance{t: t, base: fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)}
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-proc.Done():
			require.FailNowf(t, "the server exited at start", "%s", proc.FailureOutput())
		default:
		}
		resp, err := http.Get(s.base + "/health") //nolint:gosec,noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.FailNow(t, "the server did not answer", "%s", proc.Output())
	return nil
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (s *instance) do(method, path string, body []byte, contentType string, headers map[string]string) response {
	s.t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.base+path, reader)
	require.NoError(s.t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	require.NoError(s.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err)
	return response{status: resp.StatusCode, header: resp.Header, body: data}
}

func (s *instance) json(method, path string, v any, headers map[string]string) response {
	s.t.Helper()
	data, err := json.Marshal(v)
	require.NoError(s.t, err)
	return s.do(method, path, data, "application/json", headers)
}

type skillDetail struct {
	Hash   string `json:"hash"`
	Errors []struct {
		Rule string `json:"rule"`
		File string `json:"file"`
	} `json:"errors"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

func (s *instance) get(name string) skillDetail {
	s.t.Helper()
	r := s.do(http.MethodGet, "/skills/"+name, nil, "", nil)
	require.Equal(s.t, http.StatusOK, r.status, string(r.body))
	var d skillDetail
	require.NoError(s.t, json.Unmarshal(r.body, &d))
	return d
}

// Two servers share one skills directory. Both read the hash of a skill and
// write at the same time: one write wins, the other one gets 409 with the
// current hash. No server keeps state of a skill: each one reads the disk.
func TestTwoServersOnOneDirectoryRaceTwoWrites(t *testing.T) {
	t.Parallel()

	p := newProject(t)
	dags := p.dagu.ProjectPath("shared")
	require.NoError(t, os.MkdirAll(dags, 0o755))
	a, b := startServer(t, p.dagu, dags), startServer(t, p.dagu, dags)

	created := a.json(http.MethodPut, "/skills/demo", map[string]string{"content": good("demo")}, map[string]string{"If-None-Match": "*"})
	require.Equal(t, http.StatusOK, created.status, string(created.body))
	// The other server sees the skill with no sync: it reads the disk.
	require.Equal(t, a.get("demo").Hash, b.get("demo").Hash)
	// Create twice, from the other server: conflict.
	again := b.json(http.MethodPut, "/skills/demo", map[string]string{"content": good("demo")}, map[string]string{"If-None-Match": "*"})
	require.Equal(t, http.StatusConflict, again.status)

	for round := range 8 {
		hash := a.get("demo").Hash
		results := make([]response, 2)
		var wg sync.WaitGroup
		for i, s := range []*instance{a, b} {
			wg.Go(func() {
				content := strings.Replace(good("demo"), "Follow the rules.", fmt.Sprintf("Round %d writer %d.", round, i), 1)
				results[i] = s.json(http.MethodPut, "/skills/demo", map[string]string{"content": content}, map[string]string{"If-Match": hash})
			})
		}
		wg.Wait()
		statuses := []int{results[0].status, results[1].status}
		require.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, statuses, "round %d: %s | %s", round, results[0].body, results[1].body)
		for _, r := range results {
			if r.status == http.StatusConflict {
				var conflict struct {
					Code string `json:"code"`
					Hash string `json:"hash"`
				}
				require.NoError(t, json.Unmarshal(r.body, &conflict))
				require.Equal(t, "conflict", conflict.Code)
				require.Equal(t, a.get("demo").Hash, conflict.Hash, "the conflict gives the current hash")
			}
		}
	}
}

// The routes of the files and of the download through the real server: the
// hash chain, the list with an invalid skill, the ZIP of one skill and of
// several, and the refusal of an unknown name.
func TestServerFilesAndZip(t *testing.T) {
	t.Parallel()

	p := newProject(t)
	dags := p.dagu.ProjectPath("shared")
	require.NoError(t, os.MkdirAll(dags, 0o755))
	s := startServer(t, p.dagu, dags)

	create := func(name string) string {
		r := s.json(http.MethodPut, "/skills/"+name, map[string]string{"content": good(name)}, map[string]string{"If-None-Match": "*"})
		require.Equal(t, http.StatusOK, r.status, string(r.body))
		return s.get(name).Hash
	}
	putFile := func(name, rel string, data []byte, hash string) string {
		r := s.do(http.MethodPut, "/skills/"+name+"/files?path="+rel, data, "application/octet-stream", map[string]string{"If-Match": hash})
		require.Equal(t, http.StatusOK, r.status, string(r.body))
		var out struct {
			Hash string `json:"hash"`
		}
		require.NoError(t, json.Unmarshal(r.body, &out))
		return out.Hash
	}

	hash := create("alpha")
	hash = putFile("alpha", "references/guide.md", []byte("# Guide\n"), hash)
	hash = putFile("alpha", "assets/logo.bin", []byte{0, 1, 2, 255}, hash)
	hash = putFile("alpha", "scripts/run.sh", []byte("echo hi\n"), hash)
	create("beta")
	require.Equal(t, folderHash(t, filepath.Join(dags, "skills", "alpha")), hash, "the hash of the API is the canonical hash of the folder")

	// A folder that breaks the format is listed with its errors.
	require.NoError(t, os.MkdirAll(filepath.Join(dags, "skills", "broken", "references"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dags, "skills", "broken", "references", "x.md"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dags, "skills", "broken", "README.md"), []byte("x"), 0o644))
	var list struct {
		Skills []struct {
			Name   string `json:"name"`
			Valid  bool   `json:"valid"`
			Errors []struct {
				Rule string `json:"rule"`
				File string `json:"file"`
			} `json:"errors"`
		} `json:"skills"`
	}
	r := s.do(http.MethodGet, "/skills", nil, "", nil)
	require.Equal(t, http.StatusOK, r.status)
	require.NoError(t, json.Unmarshal(r.body, &list))
	require.Len(t, list.Skills, 3)
	require.Equal(t, "broken", list.Skills[2].Name)
	require.False(t, list.Skills[2].Valid)
	rules := map[string]string{}
	for _, e := range list.Skills[2].Errors {
		rules[e.Rule] = e.File
	}
	require.Equal(t, "README.md", rules["root-entry"])
	require.Contains(t, rules, "skill-md-missing")

	// A stale hash conflicts for a file too.
	stale := s.do(http.MethodPut, "/skills/alpha/files?path=references/other.md", []byte("x"), "application/octet-stream", map[string]string{"If-Match": strings.Repeat("0", 64)})
	require.Equal(t, http.StatusConflict, stale.status)
	// A write with no precondition is refused.
	none := s.do(http.MethodPut, "/skills/alpha/files?path=references/other.md", []byte("x"), "application/octet-stream", nil)
	require.Equal(t, http.StatusPreconditionRequired, none.status)

	unzip := func(data []byte) map[string][]byte {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		require.NoError(t, err)
		out := map[string][]byte{}
		for _, f := range zr.File {
			rc, err := f.Open()
			require.NoError(t, err)
			content, err := io.ReadAll(rc)
			require.NoError(t, err)
			_ = rc.Close()
			out[f.Name] = content
		}
		return out
	}

	one := s.do(http.MethodGet, "/skills/alpha/download", nil, "", nil)
	require.Equal(t, http.StatusOK, one.status)
	require.Equal(t, "application/zip", one.header.Get("Content-Type"))
	require.Equal(t, `attachment; filename="alpha.zip"`, one.header.Get("Content-Disposition"))
	files := unzip(one.body)
	require.Len(t, files, 4)
	for name, content := range files {
		want, err := os.ReadFile(filepath.Join(dags, "skills", filepath.FromSlash(name)))
		require.NoError(t, err)
		require.Equal(t, want, content, name)
		require.True(t, strings.HasPrefix(name, "alpha/"), "each skill is a top-level folder")
	}

	several := func() response {
		return s.json(http.MethodPost, "/skills/download", map[string]any{"names": []string{"beta", "broken", "alpha"}}, nil)
	}
	first := several()
	require.Equal(t, http.StatusOK, first.status)
	require.Regexp(t, `^attachment; filename="skills-\d{4}-\d{2}-\d{2}\.zip"$`, first.header.Get("Content-Disposition"))
	require.Equal(t, first.body, several().body, "the same skills give the same bytes")
	all := unzip(first.body)
	require.Contains(t, all, "broken/README.md", "an invalid skill can be downloaded")
	require.Contains(t, all, "beta/SKILL.md")
	require.Contains(t, all, "alpha/assets/logo.bin")

	missing := s.json(http.MethodPost, "/skills/download", map[string]any{"names": []string{"alpha", "nope"}}, nil)
	require.Equal(t, http.StatusNotFound, missing.status)
	require.Contains(t, string(missing.body), "nope")
	require.Equal(t, "application/json", missing.header.Get("Content-Type"), "no partial ZIP")

	// A delete needs the hash too, and removes the whole folder.
	del := s.do(http.MethodDelete, "/skills/alpha", nil, "", map[string]string{"If-Match": hash})
	require.Equal(t, http.StatusNoContent, del.status, string(del.body))
	_, err := os.Stat(filepath.Join(dags, "skills", "alpha"))
	require.True(t, os.IsNotExist(err))
}
