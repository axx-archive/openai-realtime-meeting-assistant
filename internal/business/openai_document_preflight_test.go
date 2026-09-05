package business

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type documentCountRoundTripper func(*http.Request) (*http.Response, error)

func (f documentCountRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenAIDocumentCountUsesExactFrozenRequest(t *testing.T) {
	frozen, err := FreezeOpenAIDocumentRequest("Keep supplied facts separate from assumptions.", "A useful private customer experiment.", "preparation_count_1")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport, err := NewOpenAIDocumentTransport(OpenAIDocumentTransportConfig{APIKey: "test-secret", ProjectID: "test-project", RoundTripper: documentCountRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.URL.String() != openAIDocumentEndpoint+"/input_tokens" || r.Method != "POST" || string(body) != string(frozen.Bytes()) || r.Header.Get("OpenAI-Project") != "test-project" {
			t.Fatal("count request changed frozen source or endpoint")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("unbounded count")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"object":"response.input_tokens","input_tokens":8192}`)), Header: make(http.Header)}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	count, err := transport.CountInputTokens(context.Background(), frozen)
	if err != nil || count.InputTokens != 8192 || count.RequestDigest != frozen.Digest() || count.EnvelopeDigest == "" || count.CountedAt.IsZero() || calls != 1 {
		t.Fatalf("count: %+v %v calls=%d", count, err, calls)
	}
}
func TestOpenAIDocumentCountRejectsUncertainOrExcessInput(t *testing.T) {
	frozen, _ := FreezeOpenAIDocumentRequest("Instructions", "Input", "count_2")
	cases := []struct {
		name, body string
		status     int
		failure    bool
	}{
		{"missing", `{"object":"response.input_tokens"}`, 200, false},
		{"null", `{"object":"response.input_tokens","input_tokens":null}`, 200, false},
		{"zero", `{"object":"response.input_tokens","input_tokens":0}`, 200, false},
		{"negative", `{"object":"response.input_tokens","input_tokens":-1}`, 200, false},
		{"over", `{"object":"response.input_tokens","input_tokens":8193}`, 200, false},
		{"decimal", `{"object":"response.input_tokens","input_tokens":1.5}`, 200, false},
		{"duplicate", `{"object":"response.input_tokens","input_tokens":9000,"input_tokens":1}`, 200, false},
		{"wrong object", `{"object":"response","input_tokens":1}`, 200, false},
		{"trailing", `{"object":"response.input_tokens","input_tokens":1}{}`, 200, false},
		{"oversized", strings.Repeat(" ", 8193), 200, false},
		{"redirect", "", 307, false}, {"rate limited", "", 429, false}, {"network", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			transport, _ := NewOpenAIDocumentTransport(OpenAIDocumentTransportConfig{APIKey: "test", RoundTripper: documentCountRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if tc.failure {
					return nil, errors.New("private raw provider error must not escape")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"https://example.com/"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})})
			count, err := transport.CountInputTokens(context.Background(), frozen)
			if !errors.Is(err, ErrOpenAIDocumentTokenCount) || count.InputTokens != 0 || calls != 1 {
				t.Fatalf("unexpected evidence/replay: %+v %v calls=%d", count, err, calls)
			}
		})
	}
}
