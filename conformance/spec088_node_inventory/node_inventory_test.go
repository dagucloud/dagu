// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec088_node_inventory_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
)

// node is a node as `dagu nodes list --format json` prints it.
type node struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Inventory struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"inventory"`
	Host          string            `json:"host"`
	Port          int               `json:"port"`
	User          string            `json:"user"`
	Key           map[string]string `json:"key"`
	Password      map[string]string `json:"password"`
	Bastion       string            `json:"bastion"`
	StrictHostKey bool              `json:"strict_host_key"`
	Timeout       string            `json:"timeout"`
	Labels        []string          `json:"labels"`
	Disabled      bool              `json:"disabled"`
	// Online and LastSeenAt are a worker node's.
	Online     bool   `json:"online"`
	LastSeenAt string `json:"last_seen_at"`
}

// home returns the environment of one Dagu home, which the commands of a
// test share.
func home(t *testing.T) []string {
	t.Helper()
	return []string{"DAGU_HOME=" + filepath.Join(t.TempDir(), "dagu")}
}

func listNodes(t *testing.T, dagu *harness.Runner, env []string, args ...string) []node {
	t.Helper()
	result := dagu.RunWithEnv(env, append([]string{"nodes", "list", "--format=json"}, args...)...)
	result.ExpectExitCode(0)
	var nodes []node
	require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &nodes), result.Stdout())
	return nodes
}

func nodeNames(nodes []node) []string {
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

func findNode(t *testing.T, nodes []node, name string) node {
	t.Helper()
	for _, n := range nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("node %q not listed", name)
	return node{}
}

// lineFields returns the fields of the first output line that starts with
// prefix.
func lineFields(t *testing.T, out, prefix string) []string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.Fields(line)
		}
	}
	t.Fatalf("no line starts with %q in:\n%s", prefix, out)
	return nil
}

// Each selector matches nodes by their labels with the inventory's defaults
// applied, across inventories, in name order. A disabled node is matched.
func TestListSelectsNodes(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := home(t)
	dagu.RunWithEnv(env, "nodes", "import", "inventories.json").ExpectExitCode(0)

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"Every", nil, []string{"osaka-web01", "tokyo-db01", "tokyo-jump", "tokyo-web01", "tokyo-web02"}},
		{"Key", []string{"--labels=web"}, []string{"osaka-web01", "tokyo-web01", "tokyo-web02"}},
		{"Negated", []string{"--labels=web,!canary"}, []string{"osaka-web01", "tokyo-web01"}},
		{"NodeLabelReplacesDefault", []string{"--labels=env=prod"}, []string{"tokyo-jump", "tokyo-web01", "tokyo-web02"}},
		{"WithoutCase", []string{"--labels=ENV=STG"}, []string{"osaka-web01"}},
		{"Glob", []string{"--labels=site=*,!env=prod"}, []string{"osaka-web01", "tokyo-db01"}},
		{"Inventory", []string{"--inventory=osaka STAGING"}, []string{"osaka-web01"}},
		{"InventoryAndLabels", []string{"--inventory=Tokyo production", "--labels=web"}, []string{"tokyo-web01", "tokyo-web02"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, nodeNames(listNodes(t, dagu, env, tc.args...)))
		})
	}

	nodes := listNodes(t, dagu, env)
	web := findNode(t, nodes, "tokyo-web01")
	assert.Equal(t, "ssh", web.Kind)
	assert.Equal(t, "Tokyo production", web.Inventory.Name)
	assert.Regexp(t, `^inv_[a-z2-7]{10}$`, web.Inventory.ID)
	assert.Equal(t, "deploy", web.User)
	assert.Equal(t, map[string]string{"secret": "ssh/tokyo"}, web.Key)
	assert.Equal(t, 22, web.Port)
	assert.Equal(t, "30s", web.Timeout)
	assert.True(t, web.StrictHostKey)
	assert.Equal(t, []string{"env=prod", "site=tokyo", "web"}, web.Labels)

	db := findNode(t, nodes, "tokyo-db01")
	assert.Equal(t, "postgres", db.User)
	assert.Equal(t, 2222, db.Port)
	assert.Equal(t, map[string]string{"secret": "ssh/tokyo"}, db.Key)
	assert.Equal(t, map[string]string{"secret": "ssh/tokyo-db"}, db.Password)
	assert.Equal(t, "tokyo-jump", db.Bastion)
	assert.Equal(t, []string{"db", "env=dr", "site=tokyo"}, db.Labels)

	osaka := findNode(t, nodes, "osaka-web01")
	assert.True(t, osaka.Disabled)
	assert.Equal(t, map[string]string{"file": "~/.ssh/id_ed25519"}, osaka.Key)

	table := dagu.RunWithEnv(env, "nodes", "list", "--labels=web")
	table.ExpectExitCode(0)
	assert.Equal(t, []string{"NAME", "INVENTORY", "KIND", "HOST", "LABELS", "STATUS"}, lineFields(t, table.Stdout(), "NAME"))
	assert.Equal(t, []string{"osaka-web01", "Osaka", "staging", "ssh", "10.1.0.11", "env=stg,site=osaka,web", "disabled"},
		lineFields(t, table.Stdout(), "osaka-web01"))
	assert.Equal(t, "enabled", lineFields(t, table.Stdout(), "tokyo-web01")[6])
}

func TestShowNode(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := home(t)
	dagu.RunWithEnv(env, "nodes", "import", "inventories.json").ExpectExitCode(0)

	// The name is compared without case.
	text := dagu.RunWithEnv(env, "nodes", "show", "TOKYO-DB01")
	text.ExpectExitCode(0)
	assert.Equal(t, []string{"Name", "tokyo-db01"}, lineFields(t, text.Stdout(), "Name"))
	assert.Equal(t, []string{"Key", "secret", "ssh/tokyo"}, lineFields(t, text.Stdout(), "Key"))
	assert.Equal(t, []string{"Password", "secret", "ssh/tokyo-db"}, lineFields(t, text.Stdout(), "Password"))
	assert.Equal(t, []string{"Bastion", "tokyo-jump"}, lineFields(t, text.Stdout(), "Bastion"))
	assert.Equal(t, []string{"Status", "enabled"}, lineFields(t, text.Stdout(), "Status"))

	result := dagu.RunWithEnv(env, "nodes", "show", "tokyo-db01", "--format=json")
	result.ExpectExitCode(0)
	var shown node
	require.NoError(t, json.Unmarshal([]byte(result.Stdout()), &shown))
	assert.Equal(t, findNode(t, listNodes(t, dagu, env), "tokyo-db01"), shown)

	missing := dagu.RunWithEnv(env, "nodes", "show", "tokyo-web09")
	missing.ExpectNonZeroExitCode()
	missing.ExpectStderrContains(`node "tokyo-web09" not found`)
}

// An import that breaks any rule stores nothing and lists every broken rule,
// without echoing a literal password.
func TestImportRefusesBrokenRules(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := home(t)
	dagu.RunWithEnv(env, "nodes", "import", "inventories.json").ExpectExitCode(0)

	result := dagu.RunWithEnv(env, "nodes", "import", "broken.json")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(
		"nothing imported:",
		`inventory "Nagoya": node "tokyo-web01": name: is also a node of inventory "Tokyo production"`,
		`inventory "Nagoya": node "nagoya-web01": host: "not a host" is not a DNS name`,
		`inventory "Nagoya": node "nagoya-web02": password: takes only {"secret": <ref>}`,
		`inventory "Nagoya": node "nagoya-db01": bastion: "nagoya-web03" is not a node of this inventory`,
	)
	assert.NotContains(t, result.Stderr(), "hunter2")

	assert.Len(t, listNodes(t, dagu, env), 5, "a refused import stores nothing")
	dagu.RunWithEnv(env, "nodes", "list", "--inventory=Nagoya").ExpectStderrContains(`inventory "Nagoya" not found`)
}

// An existing name is refused without --replace. --replace keeps the ID, and
// --dry-run stores nothing.
func TestImportReplacesExistingName(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	env := home(t)
	dagu.RunWithEnv(env, "nodes", "import", "inventories.json").ExpectExitCode(0)
	tokyoID := findNode(t, listNodes(t, dagu, env), "tokyo-web01").Inventory.ID

	again := dagu.RunWithEnv(env, "nodes", "import", "inventories.json")
	again.ExpectNonZeroExitCode()
	again.ExpectStderrContains(
		`inventory "Tokyo production": name: already exists`,
		`inventory "Osaka staging": name: already exists`,
	)
	// A refused inventory does not clash with the stored copy it would replace.
	assert.NotContains(t, again.Stderr(), "is also")

	dry := dagu.RunWithEnv(env, "nodes", "import", "edited.json", "--replace", "--dry-run")
	dry.ExpectExitCode(0)
	assert.Equal(t, []string{"would", "replace", "tokyo", "PRODUCTION"}, lineFields(t, dry.Stdout(), "would"))
	assert.Equal(t, []string{"tokyo-web01", "tokyo-web02"}, nodeNames(listNodes(t, dagu, env, "--labels=web,site=tokyo")))

	replaced := dagu.RunWithEnv(env, "nodes", "import", "edited.json", "--replace")
	replaced.ExpectExitCode(0)
	assert.Equal(t, []string{"replaced", "tokyo", "PRODUCTION"}, lineFields(t, replaced.Stdout(), "replaced"))
	tokyo := listNodes(t, dagu, env, "--inventory="+tokyoID)
	require.Equal(t, []string{"tokyo-web03"}, nodeNames(tokyo))
	assert.Equal(t, "tokyo PRODUCTION", tokyo[0].Inventory.Name)
}

// The export holds no IDs, and imports into another installation unchanged.
func TestExportImportsBackUnchanged(t *testing.T) {
	t.Parallel()

	dagu := harness.NewRunner(t)
	first := home(t)
	dagu.RunWithEnv(first, "nodes", "import", "inventories.json").ExpectExitCode(0)
	exported := dagu.RunWithEnv(first, "nodes", "export")
	exported.ExpectExitCode(0)
	assert.NotContains(t, exported.Stdout(), `"id"`)

	dagu.WriteFile("exported.json", exported.Stdout())
	second := home(t)
	dagu.RunWithEnv(second, "nodes", "import", "exported.json").ExpectExitCode(0)
	dagu.RunWithEnv(second, "nodes", "export").ExpectStdout(exported.Stdout())

	one := dagu.RunWithEnv(second, "nodes", "export", "--inventory=Osaka staging")
	one.ExpectExitCode(0)
	var invs []struct {
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal([]byte(one.Stdout()), &invs))
	assert.Equal(t, []struct {
		Name string `json:"name"`
	}{{Name: "Osaka staging"}}, invs)
}

func TestInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"SetID", []string{"import", "with_id.json"}, `inventory "Kobe": id: is set by Dagu and cannot be given`},
		{"UnknownField", []string{"import", "unknown_field.json"}, `invalid inventory JSON: json: unknown field "hosts"`},
		{"InvalidTerm", []string{"list", "--labels=env==prod"}, `invalid label filter "env==prod"`},
		{"EmptySelector", []string{"list", "--labels=,"}, "node selector is empty"},
		{"BlankSelector", []string{"list", "--labels= "}, "node selector is empty"},
		{"UnknownInventory", []string{"export", "--inventory=Kobe"}, `inventory "Kobe" not found`},
		{"Format", []string{"list", "--format=yaml"}, `invalid --format "yaml": use table or json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			env := home(t)
			result := dagu.RunWithEnv(env, append([]string{"nodes"}, tc.args...)...)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.want)
			assert.Empty(t, listNodes(t, dagu, env))
		})
	}
}
