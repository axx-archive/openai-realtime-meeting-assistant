package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const strideRuntimeAPIBase = "/api/stride/v1/"

func registerSTRIDERuntimeRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc(strideRuntimeAPIBase+"status", strideRuntimeStatusHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"marketplace", strideRuntimeMarketplaceHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"roster", strideRuntimeRosterHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"work", strideRuntimeWorkHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"work/", strideProductWorkSubrouteHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"marketplace/", strideProductMarketplaceSubrouteHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"roster/", strideProductRosterSubrouteHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"coworker/", strideCoworkerSubrouteHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"temporal", strideRuntimeTemporalHandler)
	mux.HandleFunc(strideRuntimeAPIBase+"temporal/answer", strideRuntimeTemporalAnswerHandler)
}

func strideRuntimeAuthenticatedRequest(w http.ResponseWriter, r *http.Request) (*userAccount, *STRIDERuntime, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil, nil, false
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return nil, nil, false
	}
	if r.URL.Query().Get("tenantId") != "" || r.URL.Query().Get("orgId") != "" {
		writeAuthError(w, http.StatusForbidden, "tenant scope is server-derived")
		return nil, nil, false
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return nil, nil, false
	}
	if kanbanApp == nil || kanbanApp.strideRuntime == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "STRIDE runtime is unavailable")
		return nil, nil, false
	}
	return user, kanbanApp.strideRuntime, true
}

func strideRuntimeStatusHandler(w http.ResponseWriter, r *http.Request) {
	_, runtime, ok := strideRuntimeAuthenticatedRequest(w, r)
	if !ok {
		return
	}
	health := runtime.Health()
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"runtime": map[string]any{
			"state": health.State, "configured": health.Configured, "restored": health.Restored,
			"generation": health.Generation, "activationFenced": true,
			"capabilities": health.Capabilities, "features": health.Features,
		},
	})
}

func strideRuntimeMarketplaceHandler(w http.ResponseWriter, r *http.Request) {
	user, runtime, ok := strideRuntimeAuthenticatedRequest(w, r)
	if !ok {
		return
	}
	listings := []STRIDEProductMarketplaceCandidate{}
	reason := "product_preview_disabled"
	canManage := isArtifactApprovalAdmin(user)
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error {
		listings = ctx.Product.candidateCatalogForViewer(canManage)
		return nil
	})
	if err == nil {
		reason = "internal_preview_only"
	} else {
		reason = strideProductRuntimeUnavailableReason(runtime, reason)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "available": false, "productReachable": err == nil, "reason": reason, "catalogMode": "internal_preview", "liveAdmissionFenced": true, "canManage": canManage, "listings": listings})
}

func strideRuntimeRosterHandler(w http.ResponseWriter, r *http.Request) {
	_, runtime, ok := strideRuntimeAuthenticatedRequest(w, r)
	if !ok {
		return
	}
	seats := []STRIDEProductTeamAgent{}
	reason := "product_preview_disabled"
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeMarketplace, func(ctx STRIDEProductContext) error { seats = ctx.Product.agentRoster(); return nil })
	if err == nil {
		reason = "provider_runtime_fenced"
	} else {
		reason = strideProductRuntimeUnavailableReason(runtime, reason)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "available": err == nil, "providerRuntimeAvailable": false, "reason": reason, "seats": seats, "recommendations": []any{}})
}

func strideRuntimeWorkHandler(w http.ResponseWriter, r *http.Request) {
	_, runtime, ok := strideRuntimeAuthenticatedRequest(w, r)
	if !ok {
		return
	}
	user := userFromRequest(r)
	principal := strideRuntimePrincipalForEmail(user.Email)
	suggestions := []STRIDEProductWorkRecord{}
	runs := []STRIDEDurableWorkRun{}
	reason := "product_preview_disabled"
	changed := false
	err := runtime.WithProductContext(canonicalTenantID(), STRIDEProductScopeWork, func(ctx STRIDEProductContext) error {
		visible, _ := ctx.Product.workForPrincipal(principal)
		for _, item := range visible {
			record, sourceCurrent, invalidated, readErr := ctx.reauthorizeWorkForRead(principal, item.ID, ctx.Receipt.IssuedAt)
			if readErr != nil {
				return readErr
			}
			changed = changed || invalidated
			suggestions = append(suggestions, record)
			if !sourceCurrent {
				continue
			}
		}
		ctx.WorkStore.mu.Lock()
		defer ctx.WorkStore.mu.Unlock()
		allowed := map[string]bool{}
		for _, item := range suggestions {
			if !item.SourceInvalidated && item.RunID != "" {
				allowed[item.RunID] = true
			}
		}
		for id, run := range ctx.WorkStore.Runs {
			if allowed[id] {
				runs = append(runs, run)
			}
		}
		return nil
	})
	if err == nil && changed {
		err = runtime.Save()
	}
	if err == nil {
		reason = "deterministic_local"
	} else {
		// Stable disabled/degraded response shapes use JSON arrays, never null.
		// Clients can keep rendering the human product surface without a special
		// nil branch while the signed STRIDE runtime remains unavailable.
		suggestions = []STRIDEProductWorkRecord{}
		runs = []STRIDEDurableWorkRun{}
		reason = strideProductRuntimeUnavailableReason(runtime, reason)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "available": err == nil, "providerExecutionFenced": true, "reason": reason, "suggestions": suggestions, "runs": runs})
}

func strideProductRuntimeUnavailableReason(runtime *STRIDERuntime, fallback string) string {
	if runtime == nil {
		return "runtime_unavailable"
	}
	switch runtime.Health().State {
	case STRIDERuntimeDisabled:
		return "runtime_disabled"
	case STRIDERuntimeUnavailable:
		return "runtime_unavailable"
	case STRIDERuntimeClosed:
		return "runtime_closed"
	default:
		return fallback
	}
}

func strideRuntimeTemporalHandler(w http.ResponseWriter, r *http.Request) {
	user, runtime, ok := strideRuntimeAuthenticatedRequest(w, r)
	if !ok {
		return
	}
	available, reason := strideRuntimeFeatureAvailability(runtime.Health(), STRIDEFeatureCrossSurfaceRetrieval)
	meetings := []any{}
	if productErr := kanbanApp.requireSTRIDETemporalProduct(); productErr == nil {
		roomID, active := kanbanApp.activeMemberConsentRoom(user.Email)
		if active {
			authority := &appMeetingSpecialistProductAuthority{app: kanbanApp, runtime: runtime}
			if scope, err := authority.ResolveScope(r.Context(), user, roomID); err == nil {
				available, reason = true, ""
				meetings = append(meetings, map[string]any{"roomId": scope.RoomID, "sittingId": scope.SittingID, "windows": []string{"last_5_minutes", "last_30_minutes"}})
			} else {
				reason = meetingSpecialistProductReason(err)
			}
		} else {
			reason = "active_member_room_required"
		}
	} else {
		reason = temporalProductErrorMessage(productErr)
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "available": available, "reason": reason, "meetings": meetings})
}

func strideRuntimeTemporalAnswerHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil || kanbanApp.strideRuntime == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "STRIDE runtime is unavailable")
		return
	}
	var payload struct {
		RoomID string `json:"roomId"`
		Window string `json:"window"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || ensureJSONEOF(decoder) != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid temporal recall request")
		return
	}
	kind, err := parseSTRIDETemporalWindow(payload.Window)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "window must be last_5_minutes or last_30_minutes")
		return
	}
	result, err := kanbanApp.answerSTRIDETemporalForMember(r.Context(), user, strings.TrimSpace(payload.RoomID), kind)
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, ErrSTRIDETemporalProductDisabled) || errors.Is(err, ErrBrainRetrievalUnavailable) || errors.Is(err, ErrSTRIDERuntimeUnavailable) {
			status = http.StatusServiceUnavailable
		}
		writeAuthError(w, status, temporalProductErrorMessage(err))
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "answer": result})
}

func strideRuntimeFeatureAvailability(health STRIDERuntimeHealth, feature STRIDEFeature) (bool, string) {
	switch health.State {
	case STRIDERuntimeDisabled:
		return false, "runtime_disabled"
	case STRIDERuntimeUnavailable:
		return false, "runtime_unavailable"
	case STRIDERuntimeClosed:
		return false, "runtime_closed"
	case STRIDERuntimeStandby:
		for _, state := range health.Features {
			if state.Feature == feature && state.Enabled {
				// The current integration has no activation receipt adapter. Do not
				// let a serialized boolean manufacture product availability.
				return false, "activation_receipt_unavailable"
			}
		}
		return false, "feature_disabled"
	default:
		return false, "runtime_unavailable"
	}
}

func strideRuntimeCapabilitySnapshot(app *kanbanBoardApp) map[string]any {
	if app == nil || app.strideRuntime == nil {
		return map[string]any{"enabled": false, "status": "disabled", "state": STRIDERuntimeDisabled, "activationFenced": true}
	}
	health := app.strideRuntime.Health()
	status := "disabled"
	if health.State == STRIDERuntimeStandby {
		// Standby proves that the durable domain runtime restored and can serve
		// authenticated inspection. It does not mean any product capability is
		// activated or provider-qualified; reporting it as healthy made an empty,
		// fully fenced product look launch-ready on the public readiness surface.
		status = "standby"
	} else if health.State == STRIDERuntimeUnavailable {
		status = "degraded"
	}
	snapshot := map[string]any{
		"enabled": health.Configured, "status": status, "state": health.State,
		"restored": health.Restored, "generation": health.Generation,
		"activationFenced": true, "domains": health.Capabilities, "features": health.Features,
	}
	// Readiness is a public aggregate surface. Never include runtime error text:
	// persistence failures can contain local paths or key/configuration details.
	// Operators get the typed state here and inspect protected logs for cause.
	return snapshot
}
