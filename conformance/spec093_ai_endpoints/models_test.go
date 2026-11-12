// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec093_ai_endpoints_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/aitest"
	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests of the models list of an endpoint (Spec 093, "The models
// list"). The server calls a fake provider with a models route. The fake records
// the headers of each call, so a test checks the key and the form of the endpoint.
//
// The automatic refresh at the period of ai.models_refresh_hours and its value 0
// take hours to observe at the binary level: the Go tests of the refresher check
// them with a fake clock.

// modelsConfig lets the server call a provider on the loopback address. The
// automatic refresh is off, so only the first read and a Reload call the fake.
const modelsConfig = `ai:
  models_refresh_hours: 0
  models_timeout_sec: 5
`

type modelsCall struct {
	Path   string
	Query  string
	Header http.Header
}

// fakeModels stands for the models API of a provider. A path that starts with
// /anthropic answers in the Anthropic form, in two pages. Another path answers in
// the OpenAI form.
type fakeModels struct {
	server *httptest.Server

	mu     sync.Mutex
	status int
	calls  []modelsCall
}

func startFakeModels(t *testing.T) *fakeModels {
	t.Helper()
	f := &fakeModels{status: http.StatusOK}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, modelsCall{Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone()})
		status := f.status
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			// An error body that echoes the credential, as some providers do.
			_, _ = w.Write([]byte(`{"error":{"message":"bad credential ` + r.Header.Get("Authorization") + r.Header.Get("X-Api-Key") + `"}}`))
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/anthropic") && r.URL.Query().Get("after_id") == "":
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","capabilities":{"effort":{"supported":true,"low":{"supported":true},"medium":{"supported":true},"high":{"supported":true},"xhigh":{"supported":false},"max":{"supported":true}}}},{"id":"claude-sonnet-5-5"}],"has_more":true,"last_id":"claude-sonnet-5-5"}`))
		case strings.HasPrefix(r.URL.Path, "/anthropic"):
			_, _ = w.Write([]byte(`{"data":[{"id":"claude-haiku-5"}],"has_more":false,"last_id":"claude-haiku-5"}`))
		default:
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5"},{"id":"gpt-5-mini"}]}`))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeModels) setStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

func (f *fakeModels) all() []modelsCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]modelsCall(nil), f.calls...)
}

// customBody is the create request of a global endpoint of the preset custom that
// lists its models at the fake. A path of "" gives an endpoint with no models URL.
func (f *fakeModels) customBody(name, protocol, auth, key, modelsPath string, dags ...string) map[string]any {
	body := map[string]any{
		"name":     name,
		"preset":   "custom",
		"protocol": protocol,
		"baseUrl":  f.server.URL,
		"auth":     auth,
		"scope":    "global",
		"allow":    map[string]any{"workspaces": []string{"*"}, "dags": dags},
		"key":      key,
	}
	if modelsPath != "" {
		body["modelsUrl"] = f.server.URL + modelsPath
	}
	return body
}

// Spec 093, "The models list": the server reads the models URL with the
// key, after the create and on each Reload. The list and its date are in the
// response. The create does not wait for the provider.
func TestTheServerReadsTheModelsListWithTheKey(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, nil)

	status, body := srv.Call(http.MethodPost, "/ai/endpoints", fake.customBody("openai1", "openai", "bearer", keyOne, "/v1/models", "*"))
	require.Equal(t, http.StatusCreated, status, body)
	assert.Contains(t, body, `"supported":true`)

	first := srv.WaitModels("openai1")
	assert.Empty(t, first.Error)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini"}, first.Models)
	assert.Equal(t, 2, first.Count)
	assert.NotEmpty(t, first.RefreshedAt, "the response has the date of the read")

	// The list of endpoints has the state of the list, with no names.
	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"count":2`)
	assert.NotContains(t, body, "gpt-5")

	// A Reload reads again, with the same key.
	time.Sleep(10 * time.Millisecond)
	second := srv.Reload("openai1")
	assert.Equal(t, first.Models, second.Models)
	assert.NotEqual(t, first.RefreshedAt, second.RefreshedAt, "a Reload gives a new date")

	calls := fake.all()
	require.Len(t, calls, 2)
	for _, call := range calls {
		assert.Equal(t, "/v1/models", call.Path)
		assert.Equal(t, "Bearer "+keyOne, call.Header.Get("Authorization"))
	}
	srv.AssertNoKey(keyOne)
}

// Spec 093, "Fields": each form of the endpoint sets its own header. The protocol
// anthropic adds anthropic-version and follows the pages of the list.
func TestTheModelsCallUsesTheFormOfTheEndpoint(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, nil)

	for _, ep := range []map[string]any{
		fake.customBody("xapi", "anthropic", "x-api-key", keyOne, "/anthropic/xapi/v1/models", "*"),
		fake.customBody("oauth", "anthropic", "anthropic-oauth", keyTwo, "/anthropic/oauth/v1/models", "*"),
		fake.customBody("bearer", "openai", "bearer", keyOne, "/openai/v1/models", "*"),
	} {
		status, body := srv.Call(http.MethodPost, "/ai/endpoints", ep)
		require.Equal(t, http.StatusCreated, status, body)
	}
	assert.Equal(t, []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5"}, srv.WaitModels("xapi").Models)
	srv.WaitModels("oauth")
	srv.WaitModels("bearer")

	byPath := map[string][]modelsCall{}
	for _, call := range fake.all() {
		byPath[call.Path] = append(byPath[call.Path], call)
	}

	xapi := byPath["/anthropic/xapi/v1/models"]
	require.Len(t, xapi, 2, "two pages")
	assert.Equal(t, "limit=1000", xapi[0].Query)
	assert.Equal(t, "after_id=claude-sonnet-5-5&limit=1000", xapi[1].Query, "the cursor is the last_id of the first page")
	for _, call := range xapi {
		assert.Equal(t, keyOne, call.Header.Get("X-Api-Key"))
		assert.Empty(t, call.Header.Get("Authorization"))
		assert.NotEmpty(t, call.Header.Get("Anthropic-Version"))
	}

	oauth := byPath["/anthropic/oauth/v1/models"]
	require.NotEmpty(t, oauth)
	assert.Equal(t, "Bearer "+keyTwo, oauth[0].Header.Get("Authorization"))
	assert.Empty(t, oauth[0].Header.Get("X-Api-Key"))
	assert.Contains(t, oauth[0].Header.Get("Anthropic-Beta"), "oauth-2025-04-20")
	assert.NotEmpty(t, oauth[0].Header.Get("Anthropic-Version"))

	bearer := byPath["/openai/v1/models"]
	require.Len(t, bearer, 1)
	assert.Equal(t, "Bearer "+keyOne, bearer[0].Header.Get("Authorization"))
	assert.Empty(t, bearer[0].Header.Get("X-Api-Key"))
	assert.Empty(t, bearer[0].Header.Get("Anthropic-Version"), "the protocol openai sends no anthropic header")
	assert.Empty(t, bearer[0].Query)
	srv.AssertNoKey(keyOne, keyTwo)
}

// Spec 093, "Refresh and reload": a failed read keeps the last good list, and
// records the error with its time. The error never has the key, also when the
// provider writes the credential in its error body.
func TestAFailedReloadKeepsTheLastGoodList(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, nil)
	status, body := srv.Call(http.MethodPost, "/ai/endpoints", fake.customBody("test-endpoint", "openai", "bearer", keyOne, "/v1/models", "*"))
	require.Equal(t, http.StatusCreated, status, body)
	good := srv.WaitModels("test-endpoint")
	require.Empty(t, good.Error)

	fake.setStatus(http.StatusInternalServerError)
	failed := srv.Reload("test-endpoint")
	assert.Equal(t, good.Models, failed.Models, "the last good list stays")
	assert.Equal(t, good.RefreshedAt, failed.RefreshedAt, "the date of the last good read stays")
	assert.Contains(t, failed.Error, "500")
	assert.NotEmpty(t, failed.ErrorAt)
	assert.Equal(t, good.Models, srv.Models("test-endpoint").Models, "a later read of the route gives the same list")
	assert.Contains(t, srv.Models("test-endpoint").Error, "500")

	// The good read after it clears the error.
	fake.setStatus(http.StatusOK)
	healed := srv.Reload("test-endpoint")
	assert.Empty(t, healed.Error)
	assert.Equal(t, good.Models, healed.Models)
	srv.AssertNoKey(keyOne)
}

// Spec 093, "The models list": an endpoint with no models URL has no list. This
// case is valid, and the endpoint still works.
func TestAnEndpointWithoutAModelsURLHasNoList(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, nil)
	status, body := srv.Call(http.MethodPost, "/ai/endpoints", fake.customBody("plain", "openai", "bearer", keyOne, "", "*"))
	require.Equal(t, http.StatusCreated, status, body)
	assert.Contains(t, body, `"supported":false`)

	m := srv.Models("plain")
	assert.False(t, m.Supported)
	assert.Empty(t, m.Models)
	assert.Empty(t, m.RefreshedAt)
	status, body = srv.Call(http.MethodPost, "/ai/endpoints/plain/models/reload", nil)
	assert.Equal(t, http.StatusBadRequest, status, body)
	assert.Contains(t, body, "no models URL")

	// The endpoint is in the choices, with an empty list, so the Model field is free text.
	status, body = srv.Call(http.MethodGet, "/ai/endpoint-choices?dag=fix-bug", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"name":"plain"`)
	assert.Contains(t, body, `"models":[]`)
	assert.Empty(t, fake.all(), "the server made no call to a provider")
}

// Spec 093, "Technical risks": a key that cannot list models, such as a token of a
// subscription, gives an error in the page, and the endpoint still works: it is
// in the choices of a DAG that may use it.
func TestAKeyThatCannotListModelsGivesAnErrorAndTheEndpointStillWorks(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	fake.setStatus(http.StatusForbidden)
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, nil)
	status, body := srv.Call(http.MethodPost, "/ai/endpoints", fake.customBody("sub", "anthropic", "anthropic-oauth", keyOne, "/anthropic/v1/models", "fix-bug"))
	require.Equal(t, http.StatusCreated, status, body)

	m := srv.WaitModels("sub")
	assert.Contains(t, m.Error, "403")
	assert.Empty(t, m.Models)
	assert.Empty(t, m.RefreshedAt)

	status, body = srv.Call(http.MethodGet, "/ai/endpoint-choices?dag=fix-bug", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"name":"sub"`)
	status, body = srv.Call(http.MethodGet, "/ai/endpoints", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"error":"the provider answered with the status 403 Forbidden"`)
	srv.AssertNoKey(keyOne)
}

// Spec 093, "Use in a step": the route of choices gives the models of an
// endpoint that the DAG may use, and no other endpoint.
func TestTheChoicesCarryTheModels(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, nil)
	for _, ep := range []map[string]any{
		fake.customBody("mine", "openai", "bearer", keyOne, "/v1/models", "fix-bug"),
		fake.customBody("theirs", "openai", "bearer", keyOne, "/v1/models", "review"),
	} {
		status, body := srv.Call(http.MethodPost, "/ai/endpoints", ep)
		require.Equal(t, http.StatusCreated, status, body)
	}
	srv.WaitModels("mine")
	srv.WaitModels("theirs")

	status, body := srv.Call(http.MethodGet, "/ai/endpoint-choices?dag=fix-bug", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `"models":["gpt-5","gpt-5-mini"]`)
	assert.Equal(t, 1, strings.Count(body, `"name":`), "the DAG sees its endpoint only")
	srv.AssertNoKey(keyOne)
}

// Spec 093, "Routes and audit": the protocol anthropic gives the display name and
// the effort levels of a model. The route of choices and the route of the list carry
// them in the field modelInfo, in the order of models. The protocol openai gives none.
func TestTheModelInfoCarriesTheDisplayNameAndTheEfforts(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, nil)
	for _, ep := range []map[string]any{
		fake.customBody("claude1", "anthropic", "x-api-key", keyOne, "/anthropic/v1/models", "fix-bug"),
		fake.customBody("plain", "openai", "bearer", keyOne, "/v1/models", "fix-bug"),
	} {
		status, body := srv.Call(http.MethodPost, "/ai/endpoints", ep)
		require.Equal(t, http.StatusCreated, status, body)
	}
	srv.WaitModels("claude1")
	srv.WaitModels("plain")

	wantClaude := `"modelInfo":[{"displayName":"Claude Opus 5.5","efforts":["low","medium","high","max"],"id":"claude-opus-5-5"},{"id":"claude-sonnet-5-5"},{"id":"claude-haiku-5"}]`
	status, body := srv.Call(http.MethodGet, "/ai/endpoints/claude1/models", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, wantClaude)

	status, body = srv.Call(http.MethodGet, "/ai/endpoint-choices?dag=fix-bug", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, wantClaude)
	assert.Contains(t, body, `"modelInfo":[{"id":"gpt-5"},{"id":"gpt-5-mini"}]`)
	srv.AssertNoKey(keyOne)
}

// Spec 093, "Routes and audit": the routes of the list need the role admin. A
// request with no token is refused with 401, and no call reaches the provider.
func TestTheModelsRoutesNeedAuthentication(t *testing.T) {
	t.Parallel()

	fake := startFakeModels(t)
	env := []string{
		"DAGU_AUTH_MODE=builtin",
		"DAGU_AUTH_BUILTIN_TOKEN_SECRET=conformance-jwt-secret",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME=admin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD=conformance-admin-password",
	}
	srv := aitest.StartServerWithConfig(t, harness.NewRunner(t), modelsConfig, env)

	status, body := srv.Call(http.MethodGet, "/ai/endpoints/test-endpoint/models", nil)
	assert.Equal(t, http.StatusUnauthorized, status, body)
	status, body = srv.Call(http.MethodPost, "/ai/endpoints/test-endpoint/models/reload", nil)
	assert.Equal(t, http.StatusUnauthorized, status, body)
	status, body = srv.CallWithToken("not-a-token", http.MethodPost, "/ai/endpoints/test-endpoint/models/reload", nil)
	assert.Equal(t, http.StatusUnauthorized, status, body)

	srv.Login("admin", "conformance-admin-password")
	status, body = srv.Call(http.MethodPost, "/ai/endpoints", fake.customBody("test-endpoint", "openai", "bearer", keyOne, "/v1/models", "*"))
	require.Equal(t, http.StatusCreated, status, body)
	srv.WaitModels("test-endpoint")
	before := len(fake.all())
	status, body = srv.Call(http.MethodPost, "/ai/endpoints/test-endpoint/models/reload", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, before+1, len(fake.all()), "only the admin reload called the provider")
	status, body = srv.Call(http.MethodPost, "/ai/endpoints/missing/models/reload", nil)
	assert.Equal(t, http.StatusNotFound, status, body)
	srv.AssertNoKey(keyOne)
}
