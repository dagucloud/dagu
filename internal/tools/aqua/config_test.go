// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"testing"

	aquaconfig "github.com/aquaproj/aqua/v2/pkg/config/aqua"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/tools"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderConfigUsesAquaPackageNames(t *testing.T) {
	t.Parallel()

	data, err := RenderConfigForPlatform(&core.ToolConfig{
		Provider: "aqua",
		Registry: &core.ToolRegistry{
			Name: "standard",
			Type: "standard",
			Ref:  "v4.233.0",
		},
		Packages: []core.ToolPackage{{
			Name:     "jq",
			Package:  "jqlang/jq",
			Version:  "jq-1.7.1",
			Commands: []string{"jq"},
		}},
	}, "linux/amd64")

	require.NoError(t, err)
	var parsed aquaconfig.Config
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	require.NotNil(t, parsed.Checksum)
	require.NotNil(t, parsed.Checksum.Enabled)
	require.NotNil(t, parsed.Checksum.RequireChecksum)
	assert.True(t, *parsed.Checksum.Enabled)
	assert.True(t, *parsed.Checksum.RequireChecksum)
	assert.Equal(t, []string{"linux/amd64"}, []string(parsed.Checksum.SupportedEnvs))
	require.Contains(t, parsed.Registries, "standard")
	assert.Equal(t, "aquaproj", parsed.Registries["standard"].RepoOwner)
	assert.Equal(t, "aqua-registry", parsed.Registries["standard"].RepoName)
	assert.Equal(t, "v4.233.0", parsed.Registries["standard"].Ref)
	require.Len(t, parsed.Packages, 1)
	assert.Equal(t, "jqlang/jq", parsed.Packages[0].Name)
	assert.Equal(t, "jq-1.7.1", parsed.Packages[0].Version)
}

func TestRenderConfigDefaultsStandardRegistry(t *testing.T) {
	t.Parallel()

	data, err := RenderConfigForPlatform(&core.ToolConfig{
		Provider: "aqua",
		Packages: []core.ToolPackage{{
			Name:     "jq",
			Package:  "jqlang/jq",
			Version:  "jq-1.7.1",
			Commands: []string{"jq"},
		}},
	}, "linux/amd64")

	require.NoError(t, err)
	var parsed aquaconfig.Config
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	require.Contains(t, parsed.Registries, "standard")
	assert.Equal(t, core.DefaultAquaStandardRegistryRef, parsed.Registries["standard"].Ref)
	assert.Regexp(t, `^[0-9a-f]{40}$`, parsed.Registries["standard"].Ref)
	require.Len(t, parsed.Packages, 1)
	assert.Equal(t, "standard", parsed.Packages[0].Registry)
}

func TestRenderConfigAcceptsPackageCommitSHA(t *testing.T) {
	t.Parallel()

	data, err := RenderConfigForPlatform(&core.ToolConfig{
		Provider: "aqua",
		Packages: []core.ToolPackage{{
			Name:     "pprof",
			Package:  "google/pprof",
			Version:  "d04f2422c8a17569c14e84da0fae252d9529826b",
			Commands: []string{"pprof"},
		}},
	}, "linux/amd64")

	require.NoError(t, err)
	var parsed aquaconfig.Config
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	require.Len(t, parsed.Packages, 1)
	assert.Equal(t, "google/pprof", parsed.Packages[0].Name)
	assert.Equal(t, "d04f2422c8a17569c14e84da0fae252d9529826b", parsed.Packages[0].Version)
}

func TestAquaParamEnforcesChecksums(t *testing.T) {
	t.Parallel()

	param := aquaParam(tools.CacheLayout{
		RootDir:      "/tmp/root",
		EnvDir:       "/tmp/env",
		ConfigFile:   "/tmp/env/aqua.yaml",
		ChecksumFile: "/tmp/env/aqua-checksums.json",
		ManifestFile: "/tmp/env/manifest.json",
	}, "/tmp/work")

	assert.True(t, param.Checksum)
	assert.True(t, param.RequireChecksum)
	assert.True(t, param.EnforceChecksum)
	assert.True(t, param.EnforceRequireChecksum)
	assert.True(t, param.DisableLazyInstall)
}

func TestRenderConfigDefaultsPackageRegistry(t *testing.T) {
	t.Parallel()

	data, err := RenderConfigForPlatform(&core.ToolConfig{
		Provider: "aqua",
		Registry: &core.ToolRegistry{
			Name:      "custom",
			Type:      "github_content",
			RepoOwner: "example",
			RepoName:  "aqua-registry",
			Ref:       "v1.0.0",
			Path:      "registry.yaml",
		},
		Packages: []core.ToolPackage{{
			Name:     "tool",
			Package:  "example/tool",
			Version:  "v1.0.0",
			Commands: []string{"tool"},
		}},
	}, "linux/amd64")

	require.NoError(t, err)
	var parsed aquaconfig.Config
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	require.Len(t, parsed.Packages, 1)
	assert.Equal(t, "custom", parsed.Packages[0].Registry)
}
