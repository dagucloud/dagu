// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/google/jsonschema-go/jsonschema"
)

const schemaHTTPTimeout = 30 * time.Second

// resolveSchemaFromParams extracts a schema declaration from params and resolves it.
// Returns (nil, nil) if no schema is declared.
func resolveSchemaFromParams(params any, workingDir, dagLocation string) (*jsonschema.Resolved, error) {
	schemaDecl, ok := extractParamsSchemaDeclaration(params)
	if !ok {
		return nil, nil
	}
	return resolveSchemaDeclaration(schemaDecl, workingDir, dagLocation)
}

// resolveSchemaDeclaration resolves a schema declaration.
// A declaration can be a path/URL string, an inline JSON Schema object, or a boolean schema.
func resolveSchemaDeclaration(schemaDecl any, workingDir, dagLocation string) (*jsonschema.Resolved, error) {
	switch v := schemaDecl.(type) {
	case nil:
		return nil, nil

	case string:
		schemaRef := strings.TrimSpace(v)
		if schemaRef == "" {
			return nil, fmt.Errorf("schema reference cannot be empty")
		}
		return getSchemaFromRef(workingDir, dagLocation, schemaRef)

	case map[string]any, bool:
		data, err := json.Marshal(schemaDecl)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal inline schema: %w", err)
		}
		return resolveSchemaData(data)

	default:
		return nil, fmt.Errorf("schema must be a string, object, or boolean, got %T", schemaDecl)
	}
}

func resolveSchemaData(schemaData []byte) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaData, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse schema JSON: %w", err)
	}

	resolvedSchema, err := schema.Resolve(&jsonschema.ResolveOptions{
		ValidateDefaults: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve schema: %w", err)
	}

	return resolvedSchema, nil
}

// Schema Ref can be a local file (relative or absolute paths), or a remote URL
func getSchemaFromRef(workingDir string, dagLocation string, schemaRef string) (*jsonschema.Resolved, error) {
	var schemaData []byte
	var err error

	// Check if it's a URL or file path
	if strings.HasPrefix(schemaRef, "http://") || strings.HasPrefix(schemaRef, "https://") {
		schemaData, err = loadSchemaFromURL(schemaRef)
	} else {
		schemaData, err = loadSchemaFromFile(workingDir, dagLocation, schemaRef)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load schema from %s: %w", schemaRef, err)
	}

	resolvedSchema, err := resolveSchemaData(schemaData)
	if err != nil {
		return nil, err
	}

	return resolvedSchema, nil
}

// loadSchemaFromURL loads a JSON schema from a URL.
func loadSchemaFromURL(schemaURL string) (data []byte, err error) {
	// Validate URL to prevent potential security issues (and satisfy linter :P)
	parsedURL, err := url.Parse(schemaURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	req, err := http.NewRequest("GET", schemaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	client := newSchemaHTTPClient()
	defer closeSchemaHTTPClient(client)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	data, err = io.ReadAll(resp.Body)
	return data, err
}

func newSchemaHTTPClient() *http.Client {
	transport := http.DefaultTransport
	if baseTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = baseTransport.Clone()
	}

	return &http.Client{
		Timeout:   schemaHTTPTimeout,
		Transport: transport,
	}
}

func closeSchemaHTTPClient(client *http.Client) {
	if client == nil || client.Transport == nil {
		return
	}
	if transport, ok := client.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}

// loadSchemaFromFile loads a JSON schema from a file path.
func loadSchemaFromFile(workingDir string, dagLocation string, filePath string) ([]byte, error) {
	// Try to resolve the schema file path in the following order:
	// 1) Current working directory (default ResolvePath behavior)
	// 2) DAG's workingDir value
	// 3) Directory of the DAG file (where it was loaded from)

	var tried []string

	// Attempts a candidate by joining base and filePath (if base provided),
	// resolving env/tilde + absolute path, checking existence, and reading.
	tryCandidate := func(label, base string) ([]byte, string, error) {
		var candidate string
		if strings.TrimSpace(base) == "" {
			candidate = filePath
		} else {
			candidate = filepath.Join(base, filePath)
		}
		resolved, err := fileutil.ResolvePath(candidate)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s: resolve error: %v", label, err))
			return nil, "", err
		}
		if !fileutil.FileExists(resolved) {
			tried = append(tried, fmt.Sprintf("%s: %s", label, resolved))
			return nil, resolved, os.ErrNotExist
		}
		data, err := fileutil.ReadFile(resolved)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s: %s (read error: %v)", label, resolved, err))
			return nil, resolved, err
		}
		return data, resolved, nil
	}

	// 1) As provided (CWD/env/tilde expansion handled by ResolvePath)
	if data, _, err := tryCandidate("cwd", ""); err == nil {
		return data, nil
	}

	// 2) From DAG's workingDir value if present
	if wd := strings.TrimSpace(workingDir); wd != "" {
		if data, _, err := tryCandidate(fmt.Sprintf("workingDir(%s)", wd), wd); err == nil {
			return data, nil
		}
	}

	// 3) From the directory of the DAG file used to build
	if dagLocation != "" {
		base := filepath.Dir(dagLocation)
		if data, _, err := tryCandidate(fmt.Sprintf("dagDir(%s)", base), base); err == nil {
			return data, nil
		}
	}

	if len(tried) == 0 {
		return nil, fmt.Errorf("failed to resolve schema file path: %s (no candidates)", filePath)
	}
	return nil, fmt.Errorf("schema file not found for %q; tried %s", filePath, strings.Join(tried, ", "))
}

// extractParamsSchemaDeclaration extracts the schema declaration from a params map.
// Returns the raw declaration and true if the params object is in schema-backed mode.
func extractParamsSchemaDeclaration(params any) (any, bool) {
	paramsMap, ok := params.(map[string]any)
	if !ok {
		return nil, false
	}
	if !isSchemaBackedParamsMap(paramsMap) {
		return nil, false
	}

	schemaDecl, hasSchema := paramsMap["schema"]
	if !hasSchema {
		return nil, false
	}

	return schemaDecl, true
}

// isSchemaBackedParamsMap reports whether a top-level params map should be
// interpreted as schema-backed params mode rather than as a legacy named-param map.
// The rules intentionally preserve legacy compatibility for maps like:
//
//	params: { schema: prod }
//	params: { schema: true }
func isSchemaBackedParamsMap(paramsMap map[string]any) bool {
	schemaDecl, hasSchema := paramsMap["schema"]
	if !hasSchema {
		return false
	}

	for key := range paramsMap {
		switch key {
		case "schema", "values":
		default:
			return false
		}
	}

	_, hasValues := paramsMap["values"]

	switch v := schemaDecl.(type) {
	case map[string]any:
		// Inline object schemas are unambiguous because legacy maps only allow scalar values.
		return true

	case bool:
		// Boolean schemas require explicit values to avoid stealing legacy maps like {schema: true}.
		return hasValues

	case string:
		if hasValues {
			return true
		}
		return looksLikeSchemaReference(v)

	default:
		return false
	}
}

func looksLikeSchemaReference(schemaRef string) bool {
	schemaRef = strings.TrimSpace(schemaRef)
	if schemaRef == "" {
		return false
	}

	return strings.Contains(schemaRef, "://") ||
		strings.Contains(schemaRef, "/") ||
		strings.Contains(schemaRef, `\`) ||
		strings.Contains(schemaRef, ".")
}
