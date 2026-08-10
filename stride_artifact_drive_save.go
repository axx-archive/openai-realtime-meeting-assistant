package main

// This surface is deliberately narrower than artifact dispositions. It can
// only copy one exact, currently authorized artifact revision into Drive. The
// separate activation receipt and store mean enabling it cannot make Discard
// reachable through either its HTTP schema or its persistence boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const artifactDriveSavePath = "/api/artifact-drive-saves/v1"

var artifactDriveSaveRuntime = struct {
	sync.Mutex
	path  string
	store *ArtifactDispositionStore
	err   error
}{}

func productionArtifactDriveSaveStore() (*ArtifactDispositionStore, error) {
	enabled, _ := strconv.ParseBool(strings.TrimSpace(os.Getenv("BONFIRE_ARTIFACT_DRIVE_SAVE_ENABLED")))
	// The action-specific switch is insufficient on its own. A reviewed
	// activation must also bind an exact receipt digest. Production currently
	// supplies neither, so this remains fail-closed.
	if !enabled || !isHexDigest(strings.TrimSpace(os.Getenv("BONFIRE_ARTIFACT_DRIVE_SAVE_ACTIVATION_RECEIPT"))) {
		return nil, ErrArtifactDispositionDisabled
	}
	path := filepath.Join(filepath.Dir(meetingMemoryPath()), "artifact-drive-saves.json")
	artifactDriveSaveRuntime.Lock()
	defer artifactDriveSaveRuntime.Unlock()
	if artifactDriveSaveRuntime.path != path || artifactDriveSaveRuntime.store == nil && artifactDriveSaveRuntime.err == nil {
		artifactDriveSaveRuntime.path = path
		artifactDriveSaveRuntime.store, artifactDriveSaveRuntime.err = OpenArtifactDispositionStore(path, true, artifactDiscardDefaultTTL)
	}
	return artifactDriveSaveRuntime.store, artifactDriveSaveRuntime.err
}

var artifactDriveSaveStoreForRequest = productionArtifactDriveSaveStore

// artifactDriveSaveEffects is an action-capability boundary, not merely an
// HTTP convention. Even an internal misuse of this effect cannot discard.
type artifactDriveSaveEffects struct {
	appArtifactDispositionEffects
}

func (effects artifactDriveSaveEffects) Save(ctx context.Context, ref ArtifactDispositionRef, actor, folderID, fileName string) (ArtifactDriveReference, error) {
	if folderID != "" {
		allowed := fileFolderManagedByUser(folderID, effects.user)
		if principal, canonical := strideE10TenantPrincipalFromContext(ctx); canonical {
			allowed = fileFolderManagedByPrincipal(folderID, principal)
		}
		if !allowed {
			return ArtifactDriveReference{}, ErrArtifactDispositionDenied
		}
	}
	return effects.appArtifactDispositionEffects.Save(ctx, ref, actor, folderID, fileName)
}

func (artifactDriveSaveEffects) Discard(context.Context, ArtifactDispositionRef, bool) (int, error) {
	return 0, ErrArtifactDispositionDenied
}

func artifactDriveSaveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	if kanbanApp == nil || kanbanApp.memory == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Save to Drive is unavailable")
		return
	}
	// Hold the server-derived current session, membership, and organization
	// principal through ACL revalidation, the durable receipt transition, the
	// Drive mutation, and the returned copy. Cutover never falls back to email.
	if !strideE10TenantSurfaceUseBound(r.Context(), StrideE10TenantSurfaceDrive) {
		err := withStrideE10TenantRequestUse(r, StrideE10TenantSurfaceDrive, func(ctx context.Context, _ *StrideE10TenantPrincipal) error {
			artifactDriveSaveHandler(w, r.WithContext(ctx))
			return nil
		})
		if err != nil {
			writeStrideE10TenantHookError(w, err, "Save to Drive is unavailable")
		}
		return
	}
	store, storeErr := artifactDriveSaveStoreForRequest()
	if r.Method == http.MethodGet {
		writeAuthJSON(w, http.StatusOK, map[string]any{
			"available":     storeErr == nil && store != nil,
			"action":        "save",
			"receiptBacked": storeErr == nil && store != nil,
		})
		return
	}
	if storeErr != nil || store == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "Save to Drive is unavailable")
		return
	}
	payload := struct {
		OperationID string                 `json:"operationId"`
		Artifact    ArtifactDispositionRef `json:"artifact"`
		FolderID    string                 `json:"folderId,omitempty"`
		FileName    string                 `json:"fileName,omitempty"`
	}{}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || ensureJSONEOF(decoder) != nil {
		writeAuthError(w, http.StatusBadRequest, ErrArtifactDispositionInvalid.Error())
		return
	}
	actorPrincipal := strideRuntimePrincipalForEmail(user.Email)
	if principal, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
		actorPrincipal = principal.PersonID
	}
	request := ArtifactDispositionRequest{
		OperationID:    payload.OperationID,
		Action:         ArtifactDispositionSave,
		ActorPrincipal: actorPrincipal,
		Artifact:       payload.Artifact,
		FolderID:       payload.FolderID,
		FileName:       strings.TrimSpace(payload.FileName),
	}
	if request.Validate() != nil {
		writeAuthError(w, http.StatusBadRequest, ErrArtifactDispositionInvalid.Error())
		return
	}
	if payload.FolderID != "" {
		folderAllowed := fileFolderManagedByUser(payload.FolderID, user)
		if principal, canonical := strideE10TenantPrincipalFromContext(r.Context()); canonical {
			folderAllowed = fileFolderManagedByPrincipal(payload.FolderID, principal)
		}
		if !folderAllowed {
			writeAuthError(w, http.StatusNotFound, "folder not found")
			return
		}
	}
	artifact, ok := authorizedArtifactForActions(r.Context(), user, payload.Artifact.ArtifactID, ACLReadContent, ACLWrite)
	if !ok {
		writeAuthError(w, http.StatusNotFound, "artifact not found")
		return
	}
	current := artifactDispositionRefFromHeader(resolveArtifactHeaderOwner(artifactAuthorizationHeaderFromEntry(artifact)))
	receipt, err := store.Apply(r.Context(), request, current, artifactDriveSaveEffects{appArtifactDispositionEffects{app: kanbanApp, user: user, artifact: artifact}})
	if err != nil {
		switch {
		case errors.Is(err, ErrArtifactDispositionConflict):
			writeAuthError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrArtifactDispositionInvalid):
			writeAuthError(w, http.StatusBadRequest, err.Error())
		default:
			writeAuthError(w, http.StatusInternalServerError, "Save to Drive failed")
		}
		return
	}
	if receipt.Action != ArtifactDispositionSave || receipt.Outcome != "saved" || receipt.Drive == nil {
		writeAuthError(w, http.StatusInternalServerError, "Save to Drive was not confirmed")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "receipt": receipt})
}
