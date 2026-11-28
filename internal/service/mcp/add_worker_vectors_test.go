// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// addWorkerVectorsPath is the file of Add worker inputs and results, from
// the module root. The Add worker dialog's tests read the same file, so the
// dialog and add_worker cannot drift apart.
var addWorkerVectorsPath = filepath.Join("ui", "src", "features", "workers", "add-worker", "__fixtures__", "commands.json")

// addWorkerVectorsOutEnv names the file to write the vectors to instead of
// checking them.
const addWorkerVectorsOutEnv = "ADD_WORKER_VECTORS_OUT"

// vectorInviteID is the invite ID the commands of every case use.
const vectorInviteID = "7c2a9f31-5b0e-4d6a-9a1e-2f3b4c5d6e7f"

// vectorInput is an Add worker input as the dialog takes it: labels are the
// text of its field, one label per comma-separated entry.
type vectorInput struct {
	Address  string `json:"address"`
	Labels   string `json:"labels"`
	Reusable bool   `json:"reusable"`
	Version  string `json:"version"`
	InviteID string `json:"inviteId"`
}

// vectorResult is what add_worker makes of a vectorInput: the refused field
// and label position, or the image and the commands.
type vectorResult struct {
	Accepted bool              `json:"accepted"`
	Field    string            `json:"field,omitempty"`
	Position int               `json:"position,omitempty"`
	Image    string            `json:"image,omitempty"`
	Commands map[string]string `json:"commands,omitempty"`
}

type vectorCase struct {
	Name   string       `json:"name"`
	Input  vectorInput  `json:"input"`
	Result vectorResult `json:"result"`
}

// vectorCases returns the cases with the results add_worker's own code
// gives them.
func vectorCases() []vectorCase {
	const address = "192.168.1.20:50055"
	const version = "2.28.0"
	byAddress := func(name, addr string) vectorCase {
		return vectorCase{Name: name, Input: vectorInput{Address: addr, Version: version, InviteID: vectorInviteID}}
	}
	byLabels := func(name, labels string) vectorCase {
		return vectorCase{Name: name, Input: vectorInput{Address: address, Labels: labels, Version: version, InviteID: vectorInviteID}}
	}
	byVersion := func(v string) vectorCase {
		return vectorCase{Name: "version " + v, Input: vectorInput{Address: address, Reusable: true, Version: v, InviteID: vectorInviteID}}
	}
	cases := []vectorCase{
		byAddress("wildcard IPv4", "0.0.0.0:50055"),
		byAddress("wildcard IPv6", "[::]:50055"),
		byAddress("loopback IPv4", "127.0.0.1:50055"),
		byAddress("loopback IPv6", "[::1]:50055"),
		byAddress("localhost", "localhost:50055"),
		byAddress("no port", "192.168.1.20"),
		byAddress("port 0", "192.168.1.20:0"),
		byAddress("container ID", "3f4e5a6b7c8d:50055"),
		byAddress("loopback with a zone", "[::1%lo0]:50055"),
		byAddress("link-local with a zone", "[fe80::1%eth0]:50055"),
		byAddress("link-local IPv6", "[fe80::1]:50055"),
		byAddress("link-local IPv4", "169.254.10.20:50055"),
		byAddress("comma", "host1,host2:50055"),
		byAddress("shell text", "$(DAGU_WORKER_INVITE):50055"),
		byAddress("multicast IPv4", "224.0.0.1:50055"),
		byAddress("multicast IPv6", "[ff02::1]:50055"),
		byAddress("broadcast", "255.255.255.255:50055"),
		byAddress("invite as the address", testWorkerInvite+":50055"),
		byAddress("mapped loopback", "[::ffff:127.0.0.1]:50055"),
		byAddress("two colons", "bad:name:50055"),
		byAddress("empty DNS label", "worker..example:50055"),
		byAddress("signed port", "worker.example:+50055"),
		byAddress("bracketed DNS name", "[worker.example]:50055"),
		byAddress("port too large", "192.168.1.20:65536"),
		byAddress("port with a leading zero", "192.168.1.20:050055"),
		byAddress("trailing dot localhost", "localhost.:50055"),
		byAddress("IPv4", address),
		byAddress("IPv6", "[2001:db8::1]:50055"),
		byAddress("DNS name", "coord.example.com:50055"),
		byAddress("single-label name", "laptop:50055"),
		byAddress("underscore name and NodePort", "worker_1.example:30055"),
		byLabels("empty label", " "),
		byLabels("same key twice", "gpu=a,gpu=b"),
		byLabels("same key in two cases", "GPU=true,gpu=false"),
		byLabels("built-in key", "os=linux"),
		byLabels("built-in arch", "arch=arm64"),
		byLabels("trailing comma", "gpu=a,"),
		byLabels("space in a value", "team=data eng"),
		byLabels("quote in a label", "note=it's"),
		byLabels("dollar in a label", "leak=$(DAGU_WORKER_INVITE)"),
		byLabels("bad label key", "gpu/type=a100"),
		byLabels("key-only and mixed case", "gpu,Zone=Tokyo-1,pool=gpu/a100"),
		byLabels("invite as a label", "gpu=true,"+testWorkerInvite),
		byLabels("repeated key names the later label", "zone=a,gpu=1,GPU=2"),
		byLabels("invite as a label value", "note="+testWorkerInvite),
		byLabels("space after a comma", "gpu=true, region=eu"),
		byLabels("empty middle entry", "gpu=true,,region=eu"),
		byLabels("spaces around =", "gpu = true,team=data/eng"),
		byLabels("no-break space before =", "gpu =true"),
		{Name: "IPv6 with a label, reusable", Input: vectorInput{Address: "[2001:db8::1]:50055", Labels: "team=data-eng", Reusable: true, Version: version, InviteID: vectorInviteID}},
		{Name: "no-break space label, reusable", Input: vectorInput{Address: address, Labels: "gpu =true", Reusable: true, Version: version, InviteID: vectorInviteID}},
		{Name: "invite ID with capitals and marks, reusable", Input: vectorInput{Address: address, Reusable: true, Version: version, InviteID: "INV_7C-2a.9F31Zq"}},
	}
	for _, v := range []string{
		"2.28.0", "v2.28.0", "v2.28.0-", "v2.28.0-rc.1", "dev", "0.0.0", "v0.0.0", "v2.27.0-12-gabc",
		"2.28.0-preview-test.1", "v2.28.0--preview.1", "2.28.0-rc_1", "v2.28.0-rc_1",
		"2.28.0+build.1", "v2.28.0-rc.1-3-g1a2b3c4d", "2.27.1-next", "release-2.28.0",
		"2.28.0-" + strings.Repeat("a", 121), "2.28.0-" + strings.Repeat("a", 122),
	} {
		cases = append(cases, byVersion(v))
	}

	position := regexp.MustCompile(`Worker label (\d+)`)
	for i := range cases {
		c := &cases[i]
		var labels []string
		if c.Input.Labels != "" {
			labels = strings.Split(c.Input.Labels, ",")
		}
		input := executeInput{AddWorker: addWorkerInput{Address: c.Input.Address, Reusable: c.Input.Reusable}, Labels: labels}
		if err := checkAddWorkerInput(input); err != nil {
			c.Result = vectorResult{Field: err.Field}
			if m := position.FindStringSubmatch(err.Message); m != nil {
				c.Result.Position, _ = strconv.Atoi(m[1])
			}
			continue
		}
		image, _ := workerImage(c.Input.Version)
		c.Result = vectorResult{
			Accepted: true,
			Image:    image,
			Commands: workerCommands(c.Input.Address, labels, c.Input.InviteID, testWorkerInvite, image, c.Input.Reusable),
		}
	}
	return cases
}

// encodeVectors returns the vectors file, one case per line, so a diff names
// the case.
func encodeVectors(t *testing.T) []byte {
	t.Helper()
	line := func(v any) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSuffix(buf.String(), "\n")
	}
	var out strings.Builder
	out.WriteString("{\n")
	out.WriteString(`  "source": ` + line("Generated from add_worker.go. Labels are the text of the dialog's field: each comma-separated entry is one label, and empty text is no label.") + ",\n")
	out.WriteString(`  "invite": ` + line(testWorkerInvite) + ",\n")
	out.WriteString("  \"cases\": [\n")
	cases := vectorCases()
	for i, c := range cases {
		out.WriteString("    " + line(c))
		if i < len(cases)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	out.WriteString("  ]\n}\n")
	return []byte(out.String())
}

// The vectors file holds what add_worker makes of each input. With
// ADD_WORKER_VECTORS_OUT set to a path, the test writes the file there
// instead, to regenerate it.
func TestAddWorkerVectors(t *testing.T) {
	want := encodeVectors(t)
	if out := os.Getenv(addWorkerVectorsOutEnv); out != "" {
		if err := os.WriteFile(out, want, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join("..", "..", "..", addWorkerVectorsPath)
	got, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s does not exist yet; it lands with the Add worker dialog", addWorkerVectorsPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs from add_worker.go; regenerate it with %s=<path>", addWorkerVectorsPath, addWorkerVectorsOutEnv)
	}
}
