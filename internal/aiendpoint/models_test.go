// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
)

const modelsKey = "sk-test-0123456789abcdefghij"

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "models", name))
	require.NoError(t, err)
	return data
}

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	data := fixtureBytes(t, name)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}
}

// customEndpoint is an endpoint of the preset custom whose models URL is the test server.
func customEndpoint(server *httptest.Server, protocol aiendpoint.Protocol, auth aiendpoint.Auth) aiendpoint.Endpoint {
	return aiendpoint.Endpoint{
		Name:      "probe",
		Preset:    aiendpoint.PresetCustom,
		Protocol:  protocol,
		BaseURL:   server.URL,
		Auth:      auth,
		ModelsURL: server.URL + "/v1/models",
	}
}

func newReader(t *testing.T, timeout time.Duration) *aiendpoint.ModelsReader {
	t.Helper()
	reader, err := aiendpoint.NewModelsReader(&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, timeout)
	require.NoError(t, err)
	return reader
}

// readNames reads the list and returns the names of the models.
func readNames(reader *aiendpoint.ModelsReader, ep aiendpoint.Endpoint, key string) ([]string, error) {
	list, err := reader.ReadList(context.Background(), ep, key)
	return list.Names, err
}

func TestModelsReadOpenAIStyle(t *testing.T) {
	var got *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		serveFixture(t, "openai.json")(w, r)
	}))
	defer server.Close()

	names, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-5", "gpt-5-mini", "text-embedding-4"}, names)
	assert.Equal(t, http.MethodGet, got.Method)
	assert.Equal(t, "/v1/models", got.URL.Path)
	assert.Empty(t, got.URL.RawQuery, "the protocol openai sends no query")
	assert.Equal(t, "Bearer "+modelsKey, got.Header.Get("Authorization"))
	assert.Empty(t, got.Header.Get("X-Api-Key"))
	assert.Empty(t, got.Header.Get("Anthropic-Version"))
}

func TestModelsReadOllamaAndGeminiNamesAreKeptAsTheyAre(t *testing.T) {
	for file, want := range map[string][]string{
		"ollama.json": {"llama3.3:70b", "qwen3-coder:30b"},
		// The OpenAI compatibility of Gemini gives the prefix models/. Dagu does not change a name.
		"gemini.json": {"models/gemini-2.5-pro", "models/gemini-2.5-flash"},
	} {
		t.Run(file, func(t *testing.T) {
			server := httptest.NewServer(serveFixture(t, file))
			defer server.Close()
			names, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), "")
			require.NoError(t, err)
			assert.Equal(t, want, names)
		})
	}
}

func TestModelsReadAnthropicFollowsThePages(t *testing.T) {
	var queries []string
	var headers []http.Header
	page1, page2 := fixtureBytes(t, "anthropic_page1.json"), fixtureBytes(t, "anthropic_page2.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		headers = append(headers, r.Header.Clone())
		if r.URL.Query().Get("after_id") == "claude-sonnet-5-5" {
			_, _ = w.Write(page2)
			return
		}
		_, _ = w.Write(page1)
	}))
	defer server.Close()

	names, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), modelsKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5"}, names)
	// The cursor of the second call is the last_id of the first page.
	assert.Equal(t, []string{"limit=1000", "after_id=claude-sonnet-5-5&limit=1000"}, queries)
	for _, h := range headers {
		assert.Equal(t, modelsKey, h.Get("X-Api-Key"))
		assert.Equal(t, aiendpoint.AnthropicVersion, h.Get("Anthropic-Version"))
		assert.Empty(t, h.Get("Authorization"))
	}
}

func TestModelsReadListAnthropicKeepsDisplayNameAndEfforts(t *testing.T) {
	server := httptest.NewServer(serveFixture(t, "anthropic_details.json"))
	defer server.Close()

	list, err := newReader(t, time.Second).ReadList(context.Background(), customEndpoint(server, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), modelsKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"claude-opus-5-5", "claude-haiku-5-5", "claude-old-3", "claude-plain"}, list.Names)
	// The efforts follow the ladder of the levels, not the order of the provider.
	// A level that is not supported is left out. A model with no detail has no item.
	assert.Equal(t, []aiendpoint.ModelInfo{
		{ID: "claude-opus-5-5", DisplayName: "Claude Opus 5.5", Efforts: []string{"low", "medium", "high", "xhigh", "max"}},
		{ID: "claude-haiku-5-5", DisplayName: "Claude Haiku 5.5", Efforts: []string{"low", "high", "max"}},
		{ID: "claude-old-3", DisplayName: "Claude Old 3"},
	}, list.Info)
}

func TestModelsReadListDropsABadDisplayName(t *testing.T) {
	long := strings.Repeat("x", aiendpoint.MaxModelNameChars+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"a","display_name":%q,"capabilities":{"effort":{"supported":true,"low":{"supported":true}}}},{"id":"b","display_name":42}]}`, long)
	}))
	defer server.Close()

	list, err := newReader(t, time.Second).ReadList(context.Background(), customEndpoint(server, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), modelsKey)
	require.NoError(t, err, "a bad display name never fails the read")
	assert.Equal(t, []string{"a", "b"}, list.Names)
	assert.Equal(t, []aiendpoint.ModelInfo{{ID: "a", Efforts: []string{"low"}}}, list.Info)
}

func TestModelsReadListOtherProtocolsHaveNoDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x","display_name":"GPT X","capabilities":{"effort":{"supported":true,"low":{"supported":true}}}}]}`))
	}))
	defer server.Close()

	list, err := newReader(t, time.Second).ReadList(context.Background(), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-x"}, list.Names)
	assert.Empty(t, list.Info)
}

func TestModelsReadAnthropicStopsAfterTenPages(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		_, _ = fmt.Fprintf(w, `{"data":[{"id":"m-%d"}],"has_more":true,"last_id":"m-%d"}`, n, n)
	}))
	defer server.Close()

	names, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), modelsKey)
	require.NoError(t, err)
	assert.EqualValues(t, aiendpoint.MaxModelsPages, calls.Load())
	assert.Len(t, names, aiendpoint.MaxModelsPages)
}

func TestModelsReadAnthropicHasMoreWithoutCursorFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m-1"}],"has_more":true}`))
	}))
	defer server.Close()

	_, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolAnthropic, aiendpoint.AuthXAPIKey), modelsKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last_id")
}

func TestModelsReadOAuthSendsTheBetaHeader(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		serveFixture(t, "empty.json")(w, r)
	}))
	defer server.Close()

	_, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolAnthropic, aiendpoint.AuthAnthropicOAuth), modelsKey)
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+modelsKey, got.Get("Authorization"))
	assert.Contains(t, got.Get("Anthropic-Beta"), "oauth-2025-04-20")
	assert.Empty(t, got.Get("X-Api-Key"))
}

func TestModelsReadEmptyListIsValid(t *testing.T) {
	server := httptest.NewServer(serveFixture(t, "empty.json"))
	defer server.Close()

	names, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.NoError(t, err)
	assert.Empty(t, names)
}

func TestModelsReadNoKeySendsNoCredential(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		serveFixture(t, "ollama.json")(w, r)
	}))
	defer server.Close()

	_, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), "")
	require.NoError(t, err)
	assert.Empty(t, got.Get("Authorization"))
}

func TestModelsReadErrorNeverHoldsTheKeyOrTheBody(t *testing.T) {
	for _, tt := range []struct {
		name    string
		status  int
		fixture string
		want    string
	}{
		{"openai 401", http.StatusUnauthorized, "error_openai_401.json", "status 401 Unauthorized"},
		{"anthropic 403", http.StatusForbidden, "error_anthropic_403.json", "status 403 Forbidden"},
		{"server error", http.StatusInternalServerError, "html.html", "status 500 Internal Server Error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := bytes.ReplaceAll(fixtureBytes(t, tt.fixture), []byte("SECRETKEY"), []byte(modelsKey))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write(body)
			}))
			defer server.Close()

			_, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.NotContains(t, err.Error(), modelsKey)
			assert.NotContains(t, err.Error(), "platform.openai.com", "the body of the provider is not in the error")
		})
	}
}

func TestModelsReadRejectsAnswersThatAreNotAList(t *testing.T) {
	for name, body := range map[string]string{
		"html":          string(fixtureBytes(t, "html.html")),
		"no data":       `{"models":[]}`,
		"null data":     `{"data":null}`,
		"data a string": `{"data":"x"}`,
		"array":         `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			_, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
			require.Error(t, err)
		})
	}
}

func TestModelsReadCapsAHugeList(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"data":[`)
	for i := range aiendpoint.MaxModels + 500 {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"model-%d"}`, i)
	}
	b.WriteString(`]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(b.String())) }))
	defer server.Close()

	names, err := readNames(newReader(t, 5*time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.NoError(t, err)
	require.Len(t, names, aiendpoint.MaxModels)
	assert.Equal(t, "model-0", names[0])
	assert.Equal(t, fmt.Sprintf("model-%d", aiendpoint.MaxModels-1), names[len(names)-1])
}

func TestModelsReadRefusesAResponseOverFourMiB(t *testing.T) {
	big := `{"data":[{"id":"` + strings.Repeat("a", aiendpoint.MaxModelsResponseBytes) + `"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(big)) }))
	defer server.Close()

	_, err := readNames(newReader(t, 5*time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than")
}

func TestModelsReadKeepsValidNamesOnce(t *testing.T) {
	long := strings.Repeat("x", aiendpoint.MaxModelNameChars+1)
	atLimit := strings.Repeat("y", aiendpoint.MaxModelNameChars)
	body := fmt.Sprintf(`{"data":[{"id":"a"},{"id":""},{"id":"a"},{"id":"%s"},{"id":"%s"},{"id":7},{"x":1},{"id":"b"}]}`, long, atLimit)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer server.Close()

	names, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", atLimit, "b"}, names)
}

func TestModelsReadDoesNotFollowARedirect(t *testing.T) {
	var other atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		other.Add(1)
		serveFixture(t, "openai.json")(w, nil)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/models", http.StatusFound)
	}))
	defer server.Close()

	_, err := readNames(newReader(t, time.Second), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirect")
	assert.Zero(t, other.Load(), "the other host gets no call, so it gets no key")
}

func TestModelsReadTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()

	_, err := readNames(newReader(t, 50*time.Millisecond), customEndpoint(server, aiendpoint.ProtocolOpenAI, aiendpoint.AuthBearer), modelsKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not answer within")
}

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestModelsReadScrubsTheKeyFromAnError(t *testing.T) {
	reader, err := aiendpoint.NewModelsReader(&http.Client{Transport: failingTransport{errors.New("dial failed for " + modelsKey)}}, time.Second)
	require.NoError(t, err)
	ep := aiendpoint.Endpoint{Name: "p", Preset: aiendpoint.PresetCustom, Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "https://example.test", Auth: aiendpoint.AuthBearer, ModelsURL: "https://example.test/v1/models"}

	_, err = readNames(reader, ep, modelsKey)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), modelsKey)
	assert.Contains(t, err.Error(), "[key]")
}

func TestModelsReadWithoutURL(t *testing.T) {
	ep := aiendpoint.Endpoint{Name: "p", Preset: aiendpoint.PresetCustom, Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "https://example.test", Auth: aiendpoint.AuthBearer}
	_, err := readNames(newReader(t, time.Second), ep, modelsKey)
	assert.ErrorIs(t, err, aiendpoint.ErrNoModelsURL)
}

func TestListURL(t *testing.T) {
	for _, tt := range []struct {
		name string
		ep   aiendpoint.Endpoint
		want string
	}{
		{"anthropic", aiendpoint.Endpoint{Preset: "anthropic", Protocol: aiendpoint.ProtocolAnthropic, BaseURL: "https://api.anthropic.com"}, "https://api.anthropic.com/v1/models"},
		{"anthropic behind a proxy follows the base URL", aiendpoint.Endpoint{Preset: "anthropic", Protocol: aiendpoint.ProtocolAnthropic, BaseURL: "https://llm.example.test/anthropic/"}, "https://llm.example.test/anthropic/v1/models"},
		{"openai", aiendpoint.Endpoint{Preset: "openai", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "https://api.openai.com/v1"}, "https://api.openai.com/v1/models"},
		{"gemini", aiendpoint.Endpoint{Preset: "google-gemini", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai"}, "https://generativelanguage.googleapis.com/v1beta/openai/models"},
		{"openrouter anthropic", aiendpoint.Endpoint{Preset: "openrouter", Protocol: aiendpoint.ProtocolAnthropic, BaseURL: "https://openrouter.ai/api"}, "https://openrouter.ai/api/v1/models"},
		{"ollama openai", aiendpoint.Endpoint{Preset: "ollama", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "http://localhost:11434/v1"}, "http://localhost:11434/v1/models"},
		{"ollama anthropic", aiendpoint.Endpoint{Preset: "ollama", Protocol: aiendpoint.ProtocolAnthropic, BaseURL: "http://gpu:11434"}, "http://gpu:11434/v1/models"},
		{"vllm openai", aiendpoint.Endpoint{Preset: "vllm", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "http://gpu:8000/v1"}, "http://gpu:8000/v1/models"},
		{"vllm anthropic", aiendpoint.Endpoint{Preset: "vllm", Protocol: aiendpoint.ProtocolAnthropic, BaseURL: "http://gpu:8000"}, "http://gpu:8000/v1/models"},
		{"litellm openai", aiendpoint.Endpoint{Preset: "litellm", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "http://gpu:4000/v1"}, "http://gpu:4000/v1/models"},
		{"zai has no list", aiendpoint.Endpoint{Preset: "zai", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "https://api.z.ai/api/paas/v4"}, ""},
		{"litellm anthropic has no list", aiendpoint.Endpoint{Preset: "litellm", Protocol: aiendpoint.ProtocolAnthropic, BaseURL: "http://gpu:4000"}, ""},
		{"custom with a URL", aiendpoint.Endpoint{Preset: "custom", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "https://a.test", ModelsURL: "https://a.test/list"}, "https://a.test/list"},
		{"custom with no URL", aiendpoint.Endpoint{Preset: "custom", Protocol: aiendpoint.ProtocolOpenAI, BaseURL: "https://a.test"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.ep.ListURL())
		})
	}
}
