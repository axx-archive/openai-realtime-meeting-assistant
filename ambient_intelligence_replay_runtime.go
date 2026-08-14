package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const ambientReplayModeEnv = "AMBIENT_INTELLIGENCE_REPLAY_MODE"

type AmbientReplayRuntimeStatus struct {
	Mode                string `json:"mode"`
	Enabled             bool   `json:"enabled"`
	Database            bool   `json:"database"`
	PlannerConfigured   bool   `json:"plannerConfigured"`
	ExecutorConfigured  bool   `json:"executorConfigured"`
	PromotionConfigured bool   `json:"promotionConfigured"`
	BoardExcluded       bool   `json:"boardExcluded"`
	MaxSources          int    `json:"maxSources"`
	Ready               bool   `json:"ready"`
	Error               string `json:"error,omitempty"`
}

var ambientReplayRuntime struct {
	sync.RWMutex
	engine *AmbientReplayEngine
	status AmbientReplayRuntimeStatus
}

var ambientReplayFenceRuntime struct {
	sync.RWMutex
	authority AmbientReplayFenceAuthority
}

func installAmbientReplayFenceAuthority(authority AmbientReplayFenceAuthority) {
	ambientReplayFenceRuntime.Lock()
	ambientReplayFenceRuntime.authority = authority
	ambientReplayFenceRuntime.Unlock()
}

func currentAmbientReplayFenceAuthority() AmbientReplayFenceAuthority {
	ambientReplayFenceRuntime.RLock()
	defer ambientReplayFenceRuntime.RUnlock()
	return ambientReplayFenceRuntime.authority
}

func configureAmbientReplayRuntime(app *kanbanBoardApp) AmbientReplayRuntimeStatus {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(ambientReplayModeEnv)))
	if mode == "" {
		mode = "off"
	}
	status := AmbientReplayRuntimeStatus{Mode: mode, BoardExcluded: true, MaxSources: ambientReplayMaxSources}
	var engine *AmbientReplayEngine
	if mode == "off" {
		status.Ready = true
	} else if mode != "plan" && mode != "execute" {
		status.Error = "invalid ambient replay mode; want off, plan, or execute"
	} else {
		status.Enabled = true
		runtime := currentCanonicalRuntime()
		authority := newProductionAmbientReplayAuthority(app, runtime)
		if runtime == nil || runtime.postgres == nil || authority == nil {
			status.Error = "canonical PostgreSQL and meeting memory are required"
		} else if authority.fences == nil {
			status.Error = "replay approval and rollback authority adapters have not been installed"
		} else {
			store := &PostgresAmbientReplayStore{pool: runtime.postgres.pool}
			promoter := newProductionAmbientReplayPromoter(app, store)
			reclaimCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			recoveryErr := error(nil)
			if promoter != nil {
				recoveryErr = promoter.RecoverAmbientReplayPromotions(reclaimCtx)
			}
			reclaimErr := error(nil)
			if recoveryErr == nil {
				_, reclaimErr = store.ReclaimExpired(reclaimCtx, time.Now().UTC())
			}
			cancel()
			if recoveryErr != nil || reclaimErr != nil {
				status.Error = "ambient replay recovery is unavailable"
			} else {
				engine = &AmbientReplayEngine{Authority: authority, Store: store, Promoter: promoter}
				status.PromotionConfigured = promoter != nil
				if runner := newProductionAmbientReplayStageRunner(app); runner != nil {
					engine.Runner = runner
					status.ExecutorConfigured = true
				}
			}
			status.Database = true
			status.PlannerConfigured = engine != nil
			if status.Error != "" {
				status.Ready = false
			} else if mode == "execute" {
				if engine == nil || engine.Runner == nil {
					status.Error = "replay executor adapter has not been installed"
				} else if engine.Promoter == nil {
					status.Error = "canonical replay promotion adapter has not been installed"
				} else {
					status.Ready = true
				}
			} else {
				status.Ready = true
			}
		}
	}
	ambientReplayRuntime.Lock()
	ambientReplayRuntime.engine, ambientReplayRuntime.status = engine, status
	ambientReplayRuntime.Unlock()
	return status
}

func installAmbientReplayStageRunner(runner AmbientReplayStageRunner) {
	ambientReplayRuntime.Lock()
	defer ambientReplayRuntime.Unlock()
	if ambientReplayRuntime.engine == nil {
		return
	}
	ambientReplayRuntime.engine.Runner = runner
	ambientReplayRuntime.status.ExecutorConfigured = runner != nil
	if ambientReplayRuntime.status.Mode == "execute" {
		ambientReplayRuntime.status.Ready = false
		if runner == nil {
			ambientReplayRuntime.status.Error = "replay executor adapter has not been installed"
		} else if !ambientReplayRuntime.status.PromotionConfigured {
			ambientReplayRuntime.status.Error = "canonical replay promotion adapter has not been installed"
		}
	}
}

func installAmbientReplayPromoter(promoter AmbientReplayPromoter) {
	ambientReplayRuntime.Lock()
	defer ambientReplayRuntime.Unlock()
	if ambientReplayRuntime.engine == nil {
		return
	}
	ambientReplayRuntime.engine.Promoter = promoter
	ambientReplayRuntime.status.PromotionConfigured = promoter != nil
	if ambientReplayRuntime.status.Mode != "execute" {
		return
	}
	ambientReplayRuntime.status.Ready = ambientReplayRuntime.engine.Runner != nil && promoter != nil
	if ambientReplayRuntime.status.Ready {
		ambientReplayRuntime.status.Error = ""
	} else if ambientReplayRuntime.engine.Runner == nil {
		ambientReplayRuntime.status.Error = "replay executor adapter has not been installed"
	} else {
		ambientReplayRuntime.status.Error = "canonical replay promotion adapter has not been installed"
	}
}

func ambientReplayRuntimeSnapshot() AmbientReplayRuntimeStatus {
	ambientReplayRuntime.RLock()
	defer ambientReplayRuntime.RUnlock()
	return ambientReplayRuntime.status
}

func currentAmbientReplayEngine() *AmbientReplayEngine {
	ambientReplayRuntime.RLock()
	defer ambientReplayRuntime.RUnlock()
	return ambientReplayRuntime.engine
}

func ambientReplayPlanHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := ambientReplayAdminRequest(w, r)
	if !ok {
		return
	}
	payload := struct {
		IdempotencyKey    string    `json:"idempotencyKey"`
		TenantID          string    `json:"tenantId"`
		RoomID            string    `json:"roomId"`
		SittingID         string    `json:"sittingId"`
		StartAfter        uint64    `json:"startAfter"`
		EndAt             uint64    `json:"endAt"`
		Stages            []string  `json:"stages"`
		MaxCalls          int       `json:"maxCalls"`
		MaxTokens         int64     `json:"maxTokens"`
		MaxCostMicros     int64     `json:"maxCostMicros"`
		ApprovalReference string    `json:"approvalReference"`
		RollbackFloor     string    `json:"rollbackFloor"`
		ExpiresAt         time.Time `json:"expiresAt"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || ensureJSONEOF(decoder) != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read exact replay plan request")
		return
	}
	tenantID, tenantErr := ambientReplayTenantForRequest(payload.TenantID)
	if tenantErr != nil {
		writeAuthError(w, http.StatusForbidden, "replay tenant does not match the authenticated workspace")
		return
	}
	engine := currentAmbientReplayEngine()
	if engine == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "ambient replay planner is unavailable")
		return
	}
	manifest, err := engine.Plan(r.Context(), AmbientReplayPlanRequest{IdempotencyKey: payload.IdempotencyKey, TenantID: tenantID, RoomID: payload.RoomID, SittingID: payload.SittingID,
		StartAfter: payload.StartAfter, EndAt: payload.EndAt, StageNames: payload.Stages, MaxCalls: payload.MaxCalls, MaxTokens: payload.MaxTokens,
		MaxCostMicros: payload.MaxCostMicros, AuthorizedBy: normalizeAccountEmail(user.Email), ApprovalReference: payload.ApprovalReference,
		RollbackFloor: payload.RollbackFloor, ExpiresAt: payload.ExpiresAt})
	if err != nil {
		writeAmbientReplayError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusCreated, map[string]any{"ok": true, "manifest": manifest})
}

func ambientReplayTenantForRequest(requested string) (string, error) {
	tenantID := canonicalTenantID()
	requested = strings.TrimSpace(requested)
	if requested != "" && requested != tenantID {
		return "", ErrAmbientReplayUnauthorized
	}
	return tenantID, nil
}

func ambientReplayExecuteHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := ambientReplayAdminRequest(w, r)
	if !ok {
		return
	}
	payload := struct {
		ManifestDigest string `json:"manifestDigest"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || ensureJSONEOF(decoder) != nil {
		writeAuthError(w, http.StatusBadRequest, "could not read exact replay execution request")
		return
	}
	engine := currentAmbientReplayEngine()
	status := ambientReplayRuntimeSnapshot()
	if engine == nil || status.Mode != "execute" || !status.Ready || !status.PromotionConfigured {
		writeAuthError(w, http.StatusServiceUnavailable, "ambient replay executor is unavailable")
		return
	}
	execution, err := engine.Execute(r.Context(), payload.ManifestDigest, normalizeAccountEmail(user.Email))
	if err != nil {
		writeAmbientReplayError(w, err)
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "execution": execution})
}

func ambientReplayAdminRequest(w http.ResponseWriter, r *http.Request) (*userAccount, bool) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return nil, false
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil, false
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil, false
	}
	if !isArtifactApprovalAdmin(user) {
		writeAuthError(w, http.StatusForbidden, "ambient replay is admin-only")
		return nil, false
	}
	return user, true
}

func writeAmbientReplayError(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	switch {
	case errors.Is(err, ErrAmbientReplayInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrAmbientReplayUnauthorized):
		status = http.StatusForbidden
	case errors.Is(err, ErrAmbientReplayUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrAmbientReplayCeiling):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, ErrAmbientReplayAlreadyActive):
		status = http.StatusConflict
	}
	writeAuthError(w, status, err.Error())
}
