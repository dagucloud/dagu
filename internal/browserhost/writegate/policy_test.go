// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate

import (
	"testing"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	r := newRules(Policy{
		SignIn:     []Rule{{Host: "login.example.com", Path: "/session", Secret: "PW"}, {Host: "sso.example.com", Path: "/oauth/*"}},
		Writes:     []Rule{{Host: "app.example.com", Path: "/items/delete"}},
		NoiseHosts: []string{"*.stats.example.com"},
	}, map[string]string{"PW": "p@ss word"})
	cases := []struct {
		name  string
		req   request
		class string
		write bool
	}{
		{"read", request{Method: "GET", URL: "https://app.example.com/items"}, "", false},
		{"post", request{Method: "POST", URL: "https://app.example.com/items"}, ClassWrite, true},
		{"delete", request{Method: "DELETE", URL: "https://app.example.com/items/1"}, ClassWrite, true},
		{"declared read", request{Method: "GET", URL: "https://app.example.com/items/delete?id=1"}, ClassWrite, true},
		{"sign-in with its secret", request{Method: "POST", URL: "https://login.example.com/session", Body: []byte("pw=p%40ss+word")}, ClassSignIn, true},
		{"sign-in without it", request{Method: "POST", URL: "https://login.example.com/session", Body: []byte("pw=guess")}, ClassWrite, true},
		{"sign-in elsewhere", request{Method: "POST", URL: "https://login.example.com/profile", Body: []byte("pw=p@ss word")}, ClassWrite, true},
		{"sign-in under a path", request{Method: "POST", URL: "https://sso.example.com/oauth/token"}, ClassSignIn, true},
		{"beacon", request{Method: "POST", URL: "https://app.example.com/log", ResourceType: "Ping"}, ClassNoise, true},
		{"report", request{Method: "POST", URL: "https://app.example.com/csp", ResourceType: "CSPViolationReport"}, ClassNoise, true},
		{"noise host", request{Method: "POST", URL: "https://eu.stats.example.com/collect"}, ClassNoise, true},
		{"not the web", request{Method: "POST", URL: "chrome-extension://abc/x"}, "", false},
	}
	for _, tc := range cases {
		class, write := r.classify(tc.req, false)
		assert.Equal(t, tc.class, class, tc.name)
		assert.Equal(t, tc.write, write, tc.name)
	}
	class, write := r.classify(request{Method: "POST", URL: "https://app.example.com/items"}, true)
	assert.Equal(t, ClassPerson, class)
	assert.True(t, write)
}

// The writes of one kind sign alike however their values change between
// runs, and writes of other kinds sign apart.
func TestSignatureStaysAcrossRuns(t *testing.T) {
	t.Parallel()

	r := newRules(Policy{}, nil)
	runs := [][]request{
		{
			{Method: "POST", URL: "https://app.example.com/api/companies/1234/deals/9b2f6c1e-7a3d-4e5f-8a9b-0c1d2e3f4a5b?_=1761460000&company_id=1234",
				ContentType: "application/json", Body: []byte(`{"company_id":1234,"issueDate":"2026-10-26","nonce":"a1b2","details":[{"amount":1000},{"amount":50}]}`)},
			{Method: "POST", URL: "https://app.example.com/Default.aspx?sid=4f2a9c", ContentType: "application/x-www-form-urlencoded",
				Body: []byte(`__EVENTTARGET=ctl00%24Save&__VIEWSTATE=abc123&rows%5B0%5D%5Bamount%5D=1`)},
			{Method: "POST", URL: "https://app.example.com/graphql", ContentType: "application/json",
				Body: []byte(`{"operationName":"CreateDeal","variables":{"input":{"ref":"Xk9fQ2LmPz"}}}`)},
			{Method: "PUT", URL: "https://app.example.com/api/r/MRCgAOBiKR/rows/fe2949", ContentType: "application/json",
				Body: []byte(`{"rows":{"YMDiNH":{"amount":1}}}`)},
		},
		{
			{Method: "POST", URL: "https://app.example.com/api/companies/987/deals/1f0e2d3c-4b5a-6978-8a9b-abcdefabcdef?_=1761469999&company_id=987",
				ContentType: "application/json", Body: []byte(`{"company_id":987,"issueDate":"2026-10-27","nonce":"ffee","details":[{"amount":7}]}`)},
			{Method: "POST", URL: "https://app.example.com/Default.aspx?sid=0b1c2d", ContentType: "application/x-www-form-urlencoded",
				Body: []byte(`__EVENTTARGET=ctl00%24Save&__VIEWSTATE=zzz999&rows%5B3%5D%5Bamount%5D=9`)},
			{Method: "POST", URL: "https://app.example.com/graphql", ContentType: "application/json",
				Body: []byte(`{"operationName":"CreateDeal","variables":{"input":{"ref":"Qr7tUv0WxY"}}}`)},
			{Method: "PUT", URL: "https://app.example.com/api/r/bHLLPDa9il/rows/7d4527", ContentType: "application/json",
				Body: []byte(`{"rows":{"Z5Igg4":{"amount":2}}}`)},
		},
	}
	sign := func(reqs []request) []string {
		var out []string
		for _, req := range reqs {
			out = append(out, r.describe(req).Signature)
		}
		return out
	}
	first := sign(runs[0])
	assert.Equal(t, first, sign(runs[1]))
	assert.Equal(t, []string{
		"POST https://app.example.com/api/companies/{n}/deals/{id}?_&company_id {company_id,details[].amount,issueDate,nonce}",
		"POST https://app.example.com/Default.aspx?sid {__EVENTTARGET,__VIEWSTATE,rows[][amount]} __EVENTTARGET=ctl00$Save",
		"POST https://app.example.com/graphql? {operationName,variables.input.ref} operationName=CreateDeal",
		"PUT https://app.example.com/api/r/{id}/rows/{id}? {rows.{key}.amount}",
	}, first)

	other := r.describe(request{Method: "POST", URL: "https://app.example.com/graphql", ContentType: "application/json",
		Body: []byte(`{"operationName":"DeleteDeal","variables":{"input":{"ref":"x"}}}`)}).Signature
	assert.NotEqual(t, first[2], other)
}

func TestDescribeHidesSensitiveValues(t *testing.T) {
	t.Parallel()

	r := newRules(Policy{}, map[string]string{"KEY": "sk-live-42"})
	cases := []struct {
		name, contentType, body, url, keeps, hides string
	}{
		{"json", "application/json", `{"user":"a","password":"hunter2","otp":123456,"note":"sk-live-42"}`, "https://x/y", `"user":"a"`, "hunter2"},
		{"json otp", "application/json", `{"user":"a","otp":123456}`, "https://x/y", `"user":"a"`, "123456"},
		{"form", "application/x-www-form-urlencoded", "user=a&csrf_token=abc&pin=0000", "https://x/y", "user=a", "abc"},
		{"form pin", "application/x-www-form-urlencoded", "user=a&pin=0000", "https://x/y", "user=a", "0000"},
		{"multipart", "multipart/form-data; boundary=B", "--B\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nhello\r\n--B\r\nContent-Disposition: form-data; name=\"authenticity_token\"\r\n\r\nzzz\r\n--B--\r\n", "https://x/y", "hello", "zzz"},
		{"cut json", "application/json", `{"user":"a","password":"hunt`, "https://x/y", `"user":"a"`, "hunt"},
		{"query", "", "", "https://x/y?user=a&access_token=abc", "user=a", "abc"},
		{"secret", "text/plain", "key is sk-live-42", "https://x/y?k=sk-live-42", "key is", "sk-live-42"},
	}
	for _, tc := range cases {
		d := r.describe(request{Method: "POST", URL: tc.url, ContentType: tc.contentType, Body: []byte(tc.body), BodyBytes: int64(len(tc.body))})
		assert.Contains(t, d.URL+" "+d.Body, tc.keeps, tc.name)
		assert.NotContains(t, d.URL+" "+d.Body, tc.hides, tc.name)
	}
}

func TestStricter(t *testing.T) {
	t.Parallel()

	assert.Equal(t, ModeRehearse, Stricter(ModeOff, ModeRehearse))
	assert.Equal(t, ModeRehearse, Stricter(ModeRehearse, ModeOff))
	assert.Equal(t, ModeOff, Stricter(ModeOff, ModeOff))
	_, err := ParseMode("loose")
	assert.Error(t, err)
}

// A declared write needs no secret; one naming a secret is refused, since
// it would never match.
func TestPolicyRefusesASecretOnAWrite(t *testing.T) {
	t.Parallel()

	assert.NoError(t, Policy{SignIn: []Rule{{Host: "login.example.com", Secret: "PW"}}}.Validate())
	err := Policy{Writes: []Rule{{Host: "app.example.com", Path: "/delete", Secret: "PW"}}}.Validate()
	assert.ErrorContains(t, err, "only sign_in rules take one")
}

// The host config takes exactly the modes the gate has.
func TestModesMatchTheHostConfig(t *testing.T) {
	t.Parallel()

	for _, mode := range cmnconfig.BrowserWriteGateModes {
		_, err := ParseMode(mode)
		assert.NoError(t, err, mode)
	}
	assert.ElementsMatch(t, []string{ModeOff, ModeRehearse}, cmnconfig.BrowserWriteGateModes)
}
