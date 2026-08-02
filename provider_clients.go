package main

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// aiProviderTransportSwitch is the one HTTP transport boundary for model and
// model-adjacent providers (OpenAI, Anthropic, and fiscal.ai). Production uses
// http.DefaultTransport. Go tests replace the delegate once, before m.Run,
// with a loopback-only transport so literal fake API keys cannot escape to a
// real provider. Keeping the switch itself on every client also prevents a
// package-init client from capturing the pre-test default.
type aiProviderTransportSwitch struct {
	mu        sync.RWMutex
	transport http.RoundTripper
}

func (transportSwitch *aiProviderTransportSwitch) RoundTrip(request *http.Request) (*http.Response, error) {
	transportSwitch.mu.RLock()
	transport := transportSwitch.transport
	transportSwitch.mu.RUnlock()
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(request)
}

func (transportSwitch *aiProviderTransportSwitch) swap(transport http.RoundTripper) func() {
	transportSwitch.mu.Lock()
	previous := transportSwitch.transport
	transportSwitch.transport = transport
	transportSwitch.mu.Unlock()
	return func() {
		transportSwitch.mu.Lock()
		transportSwitch.transport = previous
		transportSwitch.mu.Unlock()
	}
}

var sharedAIProviderHTTPTransport = &aiProviderTransportSwitch{}

// aiProviderHTTPClient must be used for every direct model-provider HTTP
// client. Endpoint-specific timeout ownership remains with the caller.
func aiProviderHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: sharedAIProviderHTTPTransport}
}

var aiProviderWebSocketState = struct {
	sync.RWMutex
	dialer *websocket.Dialer
}{}

// aiProviderRealtimeWebSocketDialer is the corresponding boundary for every
// OpenAI Realtime WebSocket, including transcription, Scout, and approved
// in-meeting specialists. It is resolved at dial time so the test fence cannot
// be bypassed by package initialization order.
func aiProviderRealtimeWebSocketDialer() *websocket.Dialer {
	aiProviderWebSocketState.RLock()
	dialer := aiProviderWebSocketState.dialer
	aiProviderWebSocketState.RUnlock()
	if dialer == nil {
		return websocket.DefaultDialer
	}
	return dialer
}

func swapAIProviderWebSocketDialer(dialer *websocket.Dialer) func() {
	aiProviderWebSocketState.Lock()
	previous := aiProviderWebSocketState.dialer
	aiProviderWebSocketState.dialer = dialer
	aiProviderWebSocketState.Unlock()
	return func() {
		aiProviderWebSocketState.Lock()
		aiProviderWebSocketState.dialer = previous
		aiProviderWebSocketState.Unlock()
	}
}
