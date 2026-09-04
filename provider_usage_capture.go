package main

// providerUsageCapture lets a caller observe the token usage of the provider
// calls made under its context — the Scout answer path stamps output tokens
// on its timing line with it. The usage ledger stays the billing authority;
// this is a per-request diagnostic tap wired at the same recordWire seam.

import (
	"context"
	"sync"
)

type providerUsageCapture struct {
	mu           sync.Mutex
	calls        int
	inputTokens  int64
	outputTokens int64
}

type providerUsageCaptureContextKey struct{}

func withProviderUsageCapture(ctx context.Context, capture *providerUsageCapture) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, providerUsageCaptureContextKey{}, capture)
}

func providerUsageCaptureFromContext(ctx context.Context) *providerUsageCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(providerUsageCaptureContextKey{}).(*providerUsageCapture)
	return capture
}

func (capture *providerUsageCapture) observe(usage *openAIResponsesUsage) {
	if capture == nil {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.calls++
	if usage != nil {
		capture.inputTokens += usage.InputTokens
		capture.outputTokens += usage.OutputTokens
	}
}

func (capture *providerUsageCapture) snapshot() (calls int, inputTokens int64, outputTokens int64) {
	if capture == nil {
		return 0, 0, 0
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.calls, capture.inputTokens, capture.outputTokens
}
