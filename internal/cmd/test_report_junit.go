// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagtest"
)

// junitTestReport is the report that --format junit writes (Spec 101, JUnit
// Report): one testsuite per test file, in run order.
type junitTestReport struct {
	suites []*junitTestSuite
	// byFile finds the suite of a test file.
	byFile map[string]*junitTestSuite
}

type junitTestSuites struct {
	XMLName  xml.Name          `xml:"testsuites"`
	Tests    int               `xml:"tests,attr"`
	Failures int               `xml:"failures,attr"`
	Errors   int               `xml:"errors,attr"`
	Time     string            `xml:"time,attr"`
	Suites   []*junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Errors   int             `xml:"errors,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
	duration time.Duration
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

// junitProblem is a failure or an error: its first line as the message, and
// every line the text report gives the case as the text.
type junitProblem struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

func newJUnitTestReport() *junitTestReport {
	return &junitTestReport{byFile: map[string]*junitTestSuite{}}
}

// suite returns the suite of a test file, added on its first case.
func (r *junitTestReport) suite(file string) *junitTestSuite {
	s, ok := r.byFile[file]
	if !ok {
		s = &junitTestSuite{Name: file}
		r.byFile[file] = s
		r.suites = append(r.suites, s)
	}
	return s
}

// addInvalidFile adds an invalid test file as a suite with one errored case
// named after the file, since none of its cases runs.
func (r *junitTestReport) addInvalidFile(file string, errs []dagtest.Finding) {
	var text strings.Builder
	for _, err := range errs {
		fmt.Fprintf(&text, "  %s\n", err)
	}
	s := r.suite(file)
	s.Tests++
	s.Errors++
	s.Cases = append(s.Cases, junitTestCase{
		Name:      file,
		Classname: file,
		Time:      junitSeconds(0),
		Error:     &junitProblem{Message: firstLine(errs), Text: text.String()},
	})
}

func (r *junitTestReport) addCase(c caseResult) {
	s := r.suite(c.file)
	s.Tests++
	s.duration += c.duration
	tc := junitTestCase{Name: c.name, Classname: c.file, Time: junitSeconds(c.duration)}
	details := caseDetails(c)
	switch c.result() {
	case resultInvalid:
		s.Errors++
		tc.Error = &junitProblem{Message: firstLine(c.errors), Text: details}
	case resultFail:
		s.Failures++
		tc.Failure = &junitProblem{Message: firstLine(c.failures), Text: details}
	default:
		tc.SystemOut = details
	}
	s.Cases = append(s.Cases, tc)
}

// write writes the XML document, ending with a newline.
func (r *junitTestReport) write(out io.Writer) error {
	doc := junitTestSuites{Suites: r.suites}
	var total time.Duration
	for _, s := range r.suites {
		s.Time = junitSeconds(s.duration)
		doc.Tests += s.Tests
		doc.Failures += s.Failures
		doc.Errors += s.Errors
		total += s.duration
	}
	doc.Time = junitSeconds(total)
	if _, err := io.WriteString(out, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(out)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(out, "\n")
	return err
}

// caseDetails returns the lines that follow the case's line in the text
// report, indented as there.
func caseDetails(c caseResult) string {
	var text strings.Builder
	(&testReport{out: &text}).writeCase(c)
	_, details, _ := strings.Cut(text.String(), "\n")
	return details
}

func firstLine[T fmt.Stringer](items []T) string {
	if len(items) == 0 {
		return ""
	}
	return items[0].String()
}

// junitSeconds writes a duration in seconds, as JUnit readers expect.
func junitSeconds(d time.Duration) string {
	return fmt.Sprintf("%.3f", d.Seconds())
}
