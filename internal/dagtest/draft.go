// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"gopkg.in/yaml.v3"
)

const (
	// draftCaseName names the one case of a draft.
	draftCaseName = "initial"
	// testFileInfix sits between a DAG file's name and its extension in
	// the name of the DAG's test file.
	testFileInfix = ".test"
	// draftPerm is the mode of a written draft before the umask. A test
	// file is committed source, not private state.
	draftPerm os.FileMode = 0o644
)

// draftExpect is the expect a draft's case holds until a run replaces it.
var draftExpect = Expect{Status: ir.Succeeded.String()}

// Draft is the test file that dagu test --init writes beside a DAG file
// (Spec 101, Authoring Flags).
type Draft struct {
	// File is the draft as LoadFile reads it once written with its case
	// expecting status: succeeded: the tested DAG and one case, which handles
	// every source of the fail-closed rule. File.Path is where Write writes
	// the draft.
	File *File
	// dag names the tested DAG file when the DAG file that the draft's name
	// points to is another one.
	dag string
	// fix holds the draft's params, its DAG-level precondition, and its
	// mocks.
	fix   caseFix
	steps *stepIndex
}

// NewDraft returns the draft of the test file for the DAG file at dagPath,
// which loads with opts as LoadFile loads a tested DAG. The draft handles
// each source of the fail-closed rule as the fix of an invalid case does,
// and gives each required param without a default its first enum value or
// "". It fails when dagPath is not a DAG file that loads, or when a test
// file for it already exists beside it.
func NewDraft(ctx context.Context, dagPath string, opts ...spec.LoadOption) (*Draft, error) {
	path, err := draftPath(dagPath)
	if err != nil {
		return nil, err
	}
	dag, findings := loadTestedDAG(ctx, dagPath, opts)
	if len(findings) > 0 {
		return nil, fmt.Errorf("%s: %s", dagPath, joinFindings(findings))
	}

	d := &Draft{steps: indexSteps(dag)}
	b := &caseBuilder{dag: dag, steps: d.steps}
	// The draft lists no step under real, so no secret needs a value and
	// the fix holds no secrets.
	d.fix = b.fix(&Case{}, checkFailClosed(dag, Handling{}).sources, nil)
	d.fix.params = b.draftParams(d.fix.params)
	if beside, _ := testedDAGPath(path, ""); beside != dagPath {
		d.dag = filepath.Base(dagPath)
	}
	decoded, errs := decodeFile(path, d.render(draftExpect))
	if len(errs) > 0 {
		return nil, fmt.Errorf("the test file drafted for %s does not decode: %v", dagPath, errs)
	}
	d.File = newFile(path, dagPath, dag, decoded)
	return d, nil
}

// Write creates the test file at File.Path with expect as the assertions of
// its case, written as Spec 101 writes an expect from a run: the run's and
// each step's attempts only when they are more than one, and notifications
// only when there is any. It fails, and changes nothing, when the file
// already exists.
func (d *Draft) Write(expect Expect) error {
	file, err := os.OpenFile(d.File.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, draftPerm) //nolint:gosec // the path is the DAG file's path with a test suffix, and a test file is readable source.
	if err != nil {
		return err
	}
	_, err = file.Write(d.render(expect))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.Join(err, fileutil.Remove(d.File.Path))
	}
	return nil
}

// draftPath returns the path of the test file for the DAG file at dagPath:
// <name>.test.yaml for <name>.yaml, and <name>.test.yml for <name>.yml. It
// fails when dagPath is not a DAG file, or when a test file for it exists
// under either suffix.
func draftPath(dagPath string) (string, error) {
	ext := filepath.Ext(dagPath)
	switch {
	case spec.IsTestFile(dagPath):
		return "", fmt.Errorf("%s is a test file; give the DAG file it tests", dagPath)
	case !slices.Contains(dagFileExtensions, ext):
		return "", fmt.Errorf("%s is not a DAG file; its name must end in .yaml or .yml", dagPath)
	}
	info, err := os.Stat(dagPath)
	switch {
	case err != nil:
		return "", err
	case info.IsDir():
		return "", fmt.Errorf("%s is a directory, not a DAG file", dagPath)
	}
	base := strings.TrimSuffix(dagPath, ext) + testFileInfix
	for _, other := range dagFileExtensions {
		if _, err := os.Stat(base + other); err == nil {
			return "", fmt.Errorf("test file %s already exists", base+other)
		}
	}
	return base + ext, nil
}

// draftParams returns the params of a draft in declaration order: the ones
// the fix gives a value, and each required param without a default.
func (b *caseBuilder) draftParams(fixed []fixValue) []fixValue {
	var params []fixValue
	for _, def := range b.dag.ParamDefs {
		handled := slices.ContainsFunc(fixed, func(v fixValue) bool { return v.name == def.Name })
		if def.Name != "" && (handled || def.Required && def.Default == nil) {
			params = append(params, fixValue{name: def.Name, value: b.paramValue(def.Name)})
		}
	}
	return params
}

// render returns the YAML of the draft with expect as its case's
// assertions.
func (d *Draft) render(expect Expect) []byte {
	root := mappingNode()
	if d.dag != "" {
		addEntry(root, "dag", valueNode(d.dag))
	}
	d.fix.addParams(root)
	d.fix.addMocks(root)
	c := mappingNode()
	addEntry(c, "name", valueNode(draftCaseName))
	if d.fix.precondition {
		addEntry(c, "precondition", valueNode(fixMet))
	}
	addEntry(c, "expect", expectNode(d.steps, expect))
	addEntry(root, "cases", &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{c}})
	return []byte(renderYAML(root))
}

// expectNode writes expect in the shape of an expect written from a run
// (Spec 101, Authoring Flags). A notification names its step by its label
// in steps.
func expectNode(steps *stepIndex, e Expect) *yaml.Node {
	n := mappingNode()
	if e.Status != "" {
		addEntry(n, "status", valueNode(e.Status))
	}
	if repeated(e.Attempts) {
		addEntry(n, "attempts", valueNode(*e.Attempts))
	}
	if len(e.Steps) > 0 {
		list := mappingNode()
		for _, s := range e.Steps {
			addEntry(list, s.Key, stepExpectNode(s))
		}
		addEntry(n, "steps", list)
	}
	if len(e.Notifications) > 0 {
		list := &yaml.Node{Kind: yaml.SequenceNode}
		for _, x := range e.Notifications {
			list.Content = append(list.Content, notificationNode(steps, x))
		}
		addEntry(n, "notifications", list)
	}
	return n
}

// stepExpectNode writes a step's status token, or a map with its status and
// attempts when they are more than one.
func stepExpectNode(s StepExpect) *yaml.Node {
	if !repeated(s.Attempts) {
		return valueNode(s.Status)
	}
	n := mappingNode()
	addEntry(n, "status", valueNode(s.Status))
	addEntry(n, "attempts", valueNode(*s.Attempts))
	return n
}

func notificationNode(steps *stepIndex, x Notification) *yaml.Node {
	n := mappingNode()
	addEntry(n, "kind", valueNode(x.Kind))
	if x.Step != "" {
		key := x.Step
		if i := slices.IndexFunc(steps.all, func(t *stepTarget) bool { return t.step.Name == x.Step }); i >= 0 {
			key = steps.all[i].label
		}
		addEntry(n, "step", valueNode(key))
	}
	if x.To != nil {
		addEntry(n, "to", valueNode(x.To))
	}
	return n
}

// repeated reports whether attempts are more than one, the counts an
// expect written from a run states.
func repeated(attempts *int) bool {
	return attempts != nil && *attempts > 1
}
