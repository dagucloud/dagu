// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
	"github.com/dagucloud/dagu/v2/internal/cmn/masking"
)

// Resource types, as DevTools names them, of requests that are not the
// page's own work.
const (
	resourcePing      = "Ping"
	resourceCSPReport = "CSPViolationReport"
	resourceDocument  = "Document"
)

// hidden replaces a sensitive value.
const hidden = masking.DefaultMaskString

// request is what the gate knows of a paused request.
type request struct {
	Method       string
	URL          string
	ResourceType string
	ContentType  string
	// Body is the start of the body; BodyBytes is its whole size, which is
	// larger when the body was cut.
	Body      []byte
	BodyBytes int64
}

// complete reports whether Body is the whole body.
func (r request) complete() bool { return int64(len(r.Body)) >= r.BodyBytes }

// rules is a policy ready to classify requests, with the values of the
// secrets its sign-in rules name.
type rules struct {
	policy  Policy
	secrets map[string]string
	masker  *masking.Masker
}

func newRules(policy Policy, secrets map[string]string) *rules {
	var entries []string
	for name, value := range secrets {
		if value == "" {
			continue
		}
		for _, form := range encodings(value) {
			entries = append(entries, name+"="+form)
		}
	}
	return &rules{policy: policy, secrets: secrets, masker: masking.NewMasker(masking.SourcedEnvVars{Secrets: entries})}
}

// encodings returns value as it is and as a form, a URL, or JSON encodes
// it.
func encodings(value string) []string {
	quoted, _ := json.Marshal(value)
	return []string{value, url.QueryEscape(value), url.PathEscape(value), string(quoted[1 : len(quoted)-1])}
}

// classify returns the class of a request and whether it is a write at
// all. A request that only reads has no class.
func (r *rules) classify(req request, person bool) (string, bool) {
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}
	if !IsWrite(req.Method) && !matchAny(r.policy.Writes, u, req, nil) {
		return "", false
	}
	switch {
	case person:
		return ClassPerson, true
	case IsWrite(req.Method) && matchAny(r.policy.SignIn, u, req, r.secrets):
		return ClassSignIn, true
	case req.ResourceType == resourcePing || req.ResourceType == resourceCSPReport ||
		hostpattern.MatchAny(u.Hostname(), r.policy.NoiseHosts):
		return ClassNoise, true
	}
	return ClassWrite, true
}

// matchAny reports whether a rule matches the request. A rule naming a
// secret matches only a request whose body carries the secret's value.
func matchAny(list []Rule, u *url.URL, req request, secrets map[string]string) bool {
	for _, rule := range list {
		if !hostpattern.Match(u.Hostname(), rule.Host) || !pathMatches(u.Path, rule.Path) {
			continue
		}
		if rule.Secret == "" || carries(req.Body, secrets[rule.Secret]) {
			return true
		}
	}
	return false
}

func pathMatches(path, pattern string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(path, prefix)
	}
	return pattern == "" || path == pattern
}

// carries reports whether body holds value as a form, a URL, or JSON
// would encode it.
func carries(body []byte, value string) bool {
	if value == "" {
		return false
	}
	for _, form := range encodings(value) {
		if bytes.Contains(body, []byte(form)) {
			return true
		}
	}
	return false
}

// description is what an event reports of a write.
type description struct {
	// URL and Body hide sensitive values.
	URL       string
	Body      string
	Fields    []string
	Signature string
}

// describe returns what an event reports of a write.
func (r *rules) describe(req request) description {
	u, err := url.Parse(req.URL)
	if err != nil {
		return description{URL: r.masker.MaskString(req.URL), Signature: req.Method + " " + req.URL}
	}
	fields, discriminators := bodyFields(req.ContentType, req.Body, req.complete())
	return description{
		URL:       r.masker.MaskString(maskQuery(u).String()),
		Body:      r.masker.MaskString(strings.ToValidUTF8(string(maskBody(req.ContentType, req.Body)), "\uFFFD")),
		Fields:    fields,
		Signature: signature(req.Method, u, fields, discriminators),
	}
}

// describeResponse returns the start of an answer's body, with sensitive
// values hidden.
func (r *rules) describeResponse(body string, base64Encoded bool, contentType string) string {
	data := []byte(body)
	if base64Encoded {
		// A cut body ends inside a group of four.
		data, _ = base64.StdEncoding.DecodeString(body[:len(body)/4*4])
	}
	return r.masker.MaskString(strings.ToValidUTF8(string(maskBody(contentType, data)), "\uFFFD"))
}

// sensitiveWords are the words of a field name whose value is always
// hidden.
var sensitiveWords = map[string]bool{
	"password": true, "passwd": true, "pass": true, "pwd": true, "pin": true, "otp": true, "totp": true,
	"mfa": true, "token": true, "csrf": true, "xsrf": true, "secret": true, "cvc": true, "cvv": true,
	"viewstate": true, "eventvalidation": true, "viewstategenerator": true,
}

// sensitiveField reports whether a field's value is hidden: its name holds
// a sensitive word, split at punctuation and at a change of case.
func sensitiveField(name string) bool {
	lower := strings.ToLower(name)
	for _, word := range []string{"password", "token", "secret", "csrf", "viewstate"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	var word []rune
	flush := func() bool {
		hit := sensitiveWords[strings.ToLower(string(word))]
		word = word[:0]
		return hit
	}
	prev := rune(0)
	for _, c := range name {
		switch {
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			if flush() {
				return true
			}
		case unicode.IsUpper(c) && unicode.IsLower(prev):
			if flush() {
				return true
			}
			word = append(word, c)
		default:
			word = append(word, c)
		}
		prev = c
	}
	return flush()
}

func maskQuery(u *url.URL) *url.URL {
	masked := *u
	if u.RawQuery == "" {
		return &masked
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return &masked
	}
	changed := false
	for name, list := range values {
		if sensitiveField(name) {
			for i := range list {
				list[i] = hidden
			}
			changed = true
		}
	}
	if changed {
		masked.RawQuery = values.Encode()
	}
	return &masked
}

var (
	jsonPair      = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"(\s*:\s*)("(?:[^"\\]|\\.)*"?|[^,}\]\s]+)`)
	formPair      = regexp.MustCompile(`(^|&)([^=&]*)=([^&]*)`)
	multipartPart = regexp.MustCompile(`(?s)(name="([^"]*)"[^\r\n]*\r\n(?:[^\r\n]+\r\n)*\r\n)(.*?)(\r\n--|$)`)
)

// maskBody hides the values of a body's sensitive fields. It works on the
// text, so a body cut short is masked too.
func maskBody(contentType string, body []byte) []byte {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		return multipartPart.ReplaceAllFunc(body, func(m []byte) []byte {
			parts := multipartPart.FindSubmatch(m)
			if !sensitiveField(string(parts[2])) {
				return m
			}
			return slices.Concat(parts[1], []byte(hidden), parts[4])
		})
	case mediaType == "application/x-www-form-urlencoded":
		return formPair.ReplaceAllFunc(body, func(m []byte) []byte {
			parts := formPair.FindSubmatch(m)
			name, _ := url.QueryUnescape(string(parts[2]))
			if !sensitiveField(name) {
				return m
			}
			return slices.Concat(parts[1], parts[2], []byte("="+hidden))
		})
	}
	trimmed := bytes.TrimSpace(body)
	if strings.Contains(mediaType, "json") || len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return jsonPair.ReplaceAllFunc(body, func(m []byte) []byte {
			parts := jsonPair.FindSubmatch(m)
			if !sensitiveField(string(parts[1])) {
				return m
			}
			return slices.Concat([]byte(`"`), parts[1], []byte(`"`), parts[2], []byte(`"`+hidden+`"`))
		})
	}
	return body
}

// Discriminators: fields whose value, not only their name, tells one kind
// of write from another.
const (
	eventTargetField   = "__EVENTTARGET"
	operationNameField = "operationName"
)

// bodyFields returns the sorted names of a body's fields, and the values of
// the fields that tell writes apart. complete reports whether body is the
// whole body; the fields of a cut JSON body are its keys as they appear.
func bodyFields(contentType string, body []byte, complete bool) ([]string, []string) {
	names := map[string]bool{}
	var discriminators []string
	mediaType, params, _ := mime.ParseMediaType(contentType)
	switch {
	case strings.HasPrefix(mediaType, "multipart/"):
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			name := templateName(part.FormName())
			if part.FileName() != "" {
				name += "(file)"
			}
			names[name] = true
		}
	case mediaType == "application/x-www-form-urlencoded":
		values, _ := url.ParseQuery(string(body))
		for name := range values {
			names[templateName(name)] = true
		}
		if target := values.Get(eventTargetField); target != "" {
			discriminators = append(discriminators, eventTargetField+"="+target)
		}
	default:
		trimmed := bytes.TrimSpace(body)
		if !strings.Contains(mediaType, "json") && (len(trimmed) == 0 || trimmed[0] != '{' && trimmed[0] != '[') {
			break
		}
		var value any
		if complete && json.Unmarshal(body, &value) == nil {
			flattenJSON("", value, names)
			if object, ok := value.(map[string]any); ok {
				if op, ok := object[operationNameField].(string); ok && op != "" {
					discriminators = append(discriminators, operationNameField+"="+op)
				}
			}
			break
		}
		for _, m := range jsonPair.FindAllSubmatch(body, -1) {
			names[templateKey(string(m[1]))] = true
		}
	}
	return sortedKeys(names), discriminators
}

func flattenJSON(prefix string, value any, names map[string]bool) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			path := templateKey(key)
			if prefix != "" {
				path = prefix + "." + path
			}
			flattenJSON(path, child, names)
		}
	case []any:
		for _, child := range v {
			flattenJSON(prefix+"[]", child, names)
		}
	default:
		if prefix != "" {
			names[prefix] = true
		}
	}
}

// signature identifies a kind of write: its method, origin, templated
// path, the names of its query and body fields, and its discriminators.
func signature(method string, u *url.URL, fields, discriminators []string) string {
	query := map[string]bool{}
	for name := range u.Query() {
		query[templateName(name)] = true
	}
	var b strings.Builder
	b.WriteString(method + " " + u.Scheme + "://" + u.Host + templatePath(u.EscapedPath()))
	b.WriteString("?" + strings.Join(sortedKeys(query), "&"))
	b.WriteString(" {" + strings.Join(fields, ",") + "}")
	for _, d := range discriminators {
		b.WriteString(" " + d)
	}
	return b.String()
}

var (
	numberSegment = regexp.MustCompile(`^\d+$`)
	uuidSegment   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hexSegment    = regexp.MustCompile(`^[0-9a-fA-F]{8,}$`)
	tokenSegment  = regexp.MustCompile(`^[A-Za-z0-9]{6,}$`)
	indexInName   = regexp.MustCompile(`\[\d+\]`)
)

// templatePath replaces the path segments that change between writes of
// one kind: numbers with {n}, and identifiers with {id}.
func templatePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		switch {
		case numberSegment.MatchString(segment):
			segments[i] = "{n}"
		case identifier(segment):
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

// identifier reports whether a segment looks generated: a UUID, a long hex
// string, or a run of letters and digits that holds several digits among
// its letters, or mixes case other than as words in camel case do.
func identifier(segment string) bool {
	if uuidSegment.MatchString(segment) || hexSegment.MatchString(segment) {
		return true
	}
	if !tokenSegment.MatchString(segment) {
		return false
	}
	digits := 0
	var upper, lower bool
	for _, c := range segment {
		switch {
		case unicode.IsDigit(c):
			digits++
		case unicode.IsUpper(c):
			upper = true
		default:
			lower = true
		}
	}
	letters := upper || lower
	return digits >= 2 && letters || upper && lower && !camelCase.MatchString(segment)
}

// camelCase matches words joined in camel case, such as accountItemId.
var camelCase = regexp.MustCompile(`^[A-Za-z][a-z]+(?:[A-Z][a-z]+)*[A-Z]?$`)

// templateName replaces the indexes of a form field's name, as in
// rows[3][amount].
func templateName(name string) string { return indexInName.ReplaceAllString(name, "[]") }

// templateKey replaces a JSON key that is an identifier or a number.
func templateKey(key string) string {
	if numberSegment.MatchString(key) || identifier(key) {
		return "{key}"
	}
	return key
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
