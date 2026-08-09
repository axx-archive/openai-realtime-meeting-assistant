package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// StrideE10W5Custody is the private product boundary for user-controlled
// personal context. Implementations must hold current person, membership, and
// session authority through their final read or write.
type StrideE10W5Custody interface {
	Put(context.Context, MyMindPrivateAuthority, string, string, string, string, int64) (MyMindPrivateSource, error)
	Correct(context.Context, MyMindPrivateAuthority, string, string, string, int64) (MyMindPrivateSource, error)
	Inspect(context.Context, MyMindPrivateAuthority) ([]MyMindPrivateSource, error)
	Forget(context.Context, MyMindPrivateAuthority, string, string, int64) error
	Export(context.Context, MyMindPrivateAuthority) (MyMindPrivateExport, error)
	Rotate(context.Context, MyMindPrivateAuthority, string) error
	DeletePerson(context.Context, MyMindPrivateAuthority, string) error
}

type StrideE10W5AuthorityResolver func(*http.Request) (MyMindPrivateAuthority, error)
type StrideE10W5FeatureGate func() bool

type strideE10W5HTTP struct {
	custody StrideE10W5Custody
	resolve StrideE10W5AuthorityResolver
	enabled StrideE10W5FeatureGate
}

func NewStrideE10W5HTTP(custody StrideE10W5Custody, resolve StrideE10W5AuthorityResolver, enabled StrideE10W5FeatureGate) http.Handler {
	return &strideE10W5HTTP{custody: custody, resolve: resolve, enabled: enabled}
}

func (h *strideE10W5HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if h == nil || h.custody == nil || h.resolve == nil || h.enabled == nil || !h.enabled() {
		writeStrideE10W5Error(w, http.StatusServiceUnavailable, "feature_unavailable")
		return
	}
	authority, err := h.resolve(r)
	if err != nil || !validMyMindPrivateAuthority(authority) {
		writeStrideE10W5Error(w, http.StatusNotFound, "not_found")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/mymind/v1")
	switch {
	case r.Method == http.MethodGet && path == "/sources":
		value, callErr := h.custody.Inspect(r.Context(), authority)
		writeStrideE10W5Result(w, value, callErr)
	case r.Method == http.MethodPost && path == "/sources":
		var input struct {
			IdempotencyKey   string `json:"idempotencyKey"`
			SourceID         string `json:"sourceId"`
			Kind             string `json:"kind"`
			Body             string `json:"body"`
			ExpectedRevision int64  `json:"expectedRevision"`
		}
		if !decodeStrideE10W5JSON(w, r, &input) {
			return
		}
		value, callErr := h.custody.Put(r.Context(), authority, input.IdempotencyKey, input.SourceID, input.Kind, input.Body, input.ExpectedRevision)
		writeStrideE10W5Result(w, value, callErr)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/correct"):
		sourceID, ok := strideE10W5SourcePath(path, "/correct")
		if !ok {
			writeStrideE10W5Error(w, http.StatusNotFound, "not_found")
			return
		}
		var input struct {
			IdempotencyKey   string `json:"idempotencyKey"`
			Body             string `json:"body"`
			ExpectedRevision int64  `json:"expectedRevision"`
		}
		if !decodeStrideE10W5JSON(w, r, &input) {
			return
		}
		value, callErr := h.custody.Correct(r.Context(), authority, input.IdempotencyKey, sourceID, input.Body, input.ExpectedRevision)
		writeStrideE10W5Result(w, value, callErr)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/forget"):
		sourceID, ok := strideE10W5SourcePath(path, "/forget")
		if !ok {
			writeStrideE10W5Error(w, http.StatusNotFound, "not_found")
			return
		}
		var input struct {
			IdempotencyKey   string `json:"idempotencyKey"`
			ExpectedRevision int64  `json:"expectedRevision"`
		}
		if !decodeStrideE10W5JSON(w, r, &input) {
			return
		}
		callErr := h.custody.Forget(r.Context(), authority, input.IdempotencyKey, sourceID, input.ExpectedRevision)
		writeStrideE10W5Result(w, map[string]any{"forgotten": callErr == nil}, callErr)
	case r.Method == http.MethodGet && path == "/export":
		value, callErr := h.custody.Export(r.Context(), authority)
		writeStrideE10W5Result(w, value, callErr)
	case r.Method == http.MethodPost && path == "/rotate":
		var input struct {
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if !decodeStrideE10W5JSON(w, r, &input) {
			return
		}
		callErr := h.custody.Rotate(r.Context(), authority, input.IdempotencyKey)
		writeStrideE10W5Result(w, map[string]any{"rotated": callErr == nil}, callErr)
	case r.Method == http.MethodDelete && path == "/person":
		var input struct {
			IdempotencyKey string `json:"idempotencyKey"`
		}
		if !decodeStrideE10W5JSON(w, r, &input) {
			return
		}
		callErr := h.custody.DeletePerson(r.Context(), authority, input.IdempotencyKey)
		writeStrideE10W5Result(w, map[string]any{"deleted": callErr == nil}, callErr)
	default:
		writeStrideE10W5Error(w, http.StatusNotFound, "not_found")
	}
}

func strideE10W5SourcePath(path, suffix string) (string, bool) {
	if !strings.HasPrefix(path, "/sources/") {
		return "", false
	}
	remainder := strings.TrimPrefix(path, "/sources/")
	if !strings.HasSuffix(remainder, suffix) {
		return "", false
	}
	value := strings.TrimSuffix(remainder, suffix)
	return value, value != "" && !strings.Contains(value, "/") && strideIdentifier(value)
}

func decodeStrideE10W5JSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		writeStrideE10W5Error(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, myMindCustodyMaxBody+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || ensureJSONEOF(decoder) != nil {
		writeStrideE10W5Error(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func writeStrideE10W5Result(w http.ResponseWriter, value any, err error) {
	if err != nil {
		switch {
		case errors.Is(err, ErrMyMindCustodyConflict):
			writeStrideE10W5Error(w, http.StatusConflict, "conflict")
		case errors.Is(err, ErrMyMindCustodyInvalid):
			writeStrideE10W5Error(w, http.StatusBadRequest, "invalid_request")
		case errors.Is(err, ErrMyMindCustodyNotFound):
			writeStrideE10W5Error(w, http.StatusNotFound, "not_found")
		default:
			writeStrideE10W5Error(w, http.StatusNotFound, "not_found")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStrideE10W5Error(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
