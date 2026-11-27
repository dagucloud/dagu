// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	bundledskills "github.com/dagucloud/dagu/v2/skills"
	"github.com/goccy/go-yaml"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	skillsExtensionID = "io.modelcontextprotocol/skills"

	methodListSkills = "skills/list"
	methodGetSkill   = "skills/get"

	skillURIScheme = "skill://"
	skillFileName  = "SKILL.md"

	// The skills change only with the binary, and a host detects a stale entry
	// from the digests on its next read, so the hint can be long.
	skillCacheTTL = time.Hour

	// Skill results are the same for every caller of a server.
	skillCacheScope = "public"

	resultTypeComplete = "complete"
)

// bundledSkills is the catalog of the skills embedded in the binary.
var bundledSkills = sync.OnceValue(func() *skillCatalog {
	catalog, err := newSkillCatalog(bundledskills.Assets, bundledskills.DaguSkillDir)
	if err != nil {
		panic(fmt.Sprintf("mcp: bundled skill %s: %v", bundledskills.DaguSkillDir, err))
	}
	return catalog
})

// skillCatalog holds the skills a server serves, built once so that requests
// never read files.
type skillCatalog struct {
	skills []*skillEntry
	files  []skillFile
}

// skillEntry is the Skill object of the skills extension.
type skillEntry struct {
	URI         string          `json:"uri"`
	Frontmatter json.RawMessage `json:"frontmatter"`
	Resources   []skillResource `json:"resources"`
}

// skillResource is the SkillResource object of the skills extension.
type skillResource struct {
	URI    string `json:"uri"`
	Digest string `json:"digest"`
	Size   int    `json:"size"`
}

// skillFile is one file of a skill, served as a resource.
type skillFile struct {
	uri         string
	name        string
	description string
	text        string
}

type listSkillsParams struct {
	mcpsdk.ParamsBase
	Cursor string `json:"cursor,omitempty"`
}

type listSkillsResult struct {
	mcpsdk.ResultBase
	mcpsdk.Cacheable
	ResultType string        `json:"resultType"`
	Skills     []*skillEntry `json:"skills"`
}

type getSkillParams struct {
	mcpsdk.ParamsBase
	URI string `json:"uri"`
}

type getSkillResult struct {
	mcpsdk.ResultBase
	mcpsdk.Cacheable
	ResultType string      `json:"resultType"`
	Skill      *skillEntry `json:"skill"`
}

// newSkillCatalog reads the skill in directory dir of fsys. The skill path in
// its URIs is the base name of dir, which must equal the name in SKILL.md.
func newSkillCatalog(fsys fs.FS, dir string) (*skillCatalog, error) {
	skillPath := path.Base(dir)
	entry := &skillEntry{URI: skillURIScheme + skillPath + "/" + skillFileName}
	catalog := &skillCatalog{skills: []*skillEntry{entry}}

	err := fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		// Resource text must carry the exact bytes the digest covers.
		if !utf8.Valid(data) {
			return fmt.Errorf("%s is not valid UTF-8", name)
		}
		rel := strings.TrimPrefix(name, dir+"/")
		file := skillFile{
			uri:  skillURIScheme + skillPath + "/" + rel,
			name: rel,
			text: string(data),
		}
		if rel == skillFileName {
			frontmatter, skillName, description, err := parseSkillFrontmatter(data)
			if err != nil {
				return err
			}
			if skillName != skillPath {
				return fmt.Errorf("name %q differs from the skill path %q", skillName, skillPath)
			}
			entry.Frontmatter = frontmatter
			file.name, file.description = skillName, description
		}
		sum := sha256.Sum256(data)
		entry.Resources = append(entry.Resources, skillResource{
			URI:    file.uri,
			Digest: "sha256:" + hex.EncodeToString(sum[:]),
			Size:   len(data),
		})
		catalog.files = append(catalog.files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if entry.Frontmatter == nil {
		return nil, errors.New(skillFileName + " is missing")
	}
	return catalog, nil
}

// parseSkillFrontmatter returns the YAML frontmatter of SKILL.md as a JSON
// object, with its name and description.
func parseSkillFrontmatter(data []byte) (json.RawMessage, string, string, error) {
	const fence = "---\n"
	rest, ok := bytes.CutPrefix(data, []byte(fence))
	if !ok {
		return nil, "", "", errors.New("frontmatter must start with a --- line")
	}
	front, _, ok := bytes.Cut(rest, []byte("\n"+fence))
	if !ok {
		return nil, "", "", errors.New("frontmatter has no closing --- line")
	}
	var fields map[string]any
	if err := yaml.Unmarshal(front, &fields); err != nil {
		return nil, "", "", fmt.Errorf("frontmatter: %w", err)
	}
	name, _ := fields["name"].(string)
	description, _ := fields["description"].(string)
	if name == "" || description == "" {
		return nil, "", "", errors.New("frontmatter needs name and description")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, "", "", fmt.Errorf("frontmatter: %w", err)
	}
	return raw, name, description, nil
}

// registerSkills serves the catalog through the skills extension: each file as
// a resource, plus the skills/list and skills/get methods.
func registerSkills(server *mcpsdk.Server, catalog *skillCatalog) error {
	for _, file := range catalog.files {
		server.AddResource(&mcpsdk.Resource{
			URI:         file.uri,
			Name:        file.name,
			Description: file.description,
			MIMEType:    resourceMIMEText,
		}, func(context.Context, *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			return &mcpsdk.ReadResourceResult{
				Cacheable: skillCacheable(),
				Contents: []*mcpsdk.ResourceContents{{
					URI:      file.uri,
					MIMEType: resourceMIMEText,
					Text:     file.text,
				}},
			}, nil
		})
	}
	if err := mcpsdk.AddReceivingCustomMethod(server, methodListSkills, catalog.list); err != nil {
		return err
	}
	return mcpsdk.AddReceivingCustomMethod(server, methodGetSkill, catalog.get)
}

func (c *skillCatalog) list(_ context.Context, _ *mcpsdk.ServerSession, params *listSkillsParams) (*listSkillsResult, error) {
	// The catalog fits one page, so no cursor is ever issued.
	if params != nil && params.Cursor != "" {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Invalid cursor"}
	}
	return &listSkillsResult{
		Cacheable:  skillCacheable(),
		ResultType: resultTypeComplete,
		Skills:     c.skills,
	}, nil
}

func (c *skillCatalog) get(_ context.Context, _ *mcpsdk.ServerSession, params *getSkillParams) (*getSkillResult, error) {
	var uri string
	if params != nil {
		uri = params.URI
	}
	for _, entry := range c.skills {
		if entry.URI == uri {
			return &getSkillResult{
				Cacheable:  skillCacheable(),
				ResultType: resultTypeComplete,
				Skill:      entry,
			}, nil
		}
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "No skill is served at " + uri}
}

func skillCacheable() mcpsdk.Cacheable {
	return mcpsdk.Cacheable{TTLMs: int(skillCacheTTL / time.Millisecond), CacheScope: skillCacheScope}
}
