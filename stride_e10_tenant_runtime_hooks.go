package main

import (
	"context"
	"errors"
	"net/http"
)

type strideE10TenantPrincipalContextKey struct{}
type strideE10TenantSurfaceContextKey struct{}

// StrideE10TenantRuntimeHookCoverage is the executable runtime-hook inventory.
// It intentionally lives outside the converter's frozen migration manifest so
// hook batches can land independently and the tenant owner can reconcile the
// durable plan only after focused tests pass. Active means the local callback
// fence is executable; W3 production bootstrap remains off/shadow-only until
// W4 installs durable canonical organization and operation stores.
func StrideE10TenantRuntimeHookCoverage() []StrideE10TenantSurfaceCoverage {
	return []StrideE10TenantSurfaceCoverage{
		{Surface: StrideE10TenantSurfaceAuthSession, HookStatus: StrideE10TenantHookActive},
		{Surface: StrideE10TenantSurfaceHTTP, HookStatus: StrideE10TenantHookActive},
		{Surface: StrideE10TenantSurfaceWebSocket, LegacySingletons: []string{"cutover socket is unavailable until tenant-native room state lands"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceChat, LegacySingletons: []string{"cutover chat is unavailable while owner-email rows remain"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceRoomAdmission, LegacySingletons: []string{"cutover room admission is unavailable while fixed roster authority remains"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceBoard, LegacySingletons: []string{"cutover Board is unavailable while singleton board authority remains"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceDrive, HookStatus: StrideE10TenantHookActive},
		{Surface: StrideE10TenantSurfacePushDelivery, HookStatus: StrideE10TenantHookActive},
		{Surface: StrideE10TenantSurfaceNotification, HookStatus: StrideE10TenantHookActive},
		{Surface: StrideE10TenantSurfaceArtifactACL, HookStatus: StrideE10TenantHookActive},
		{Surface: StrideE10TenantSurfaceWorkQueue, LegacySingletons: []string{"goal HTTP ingress has no context-to-purpose envelope mint"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceWorker, LegacySingletons: []string{"worker envelope fence exists but no active canonical production ingress"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceScout, LegacySingletons: []string{"cutover Scout ingress remains unavailable until canonical source adapters land"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceBrain, LegacySingletons: []string{"scheduled brain, board, replay, and projection roots have no originating canonical admission"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceProductContext, LegacySingletons: []string{"legacy product context still accepts singleton tenant"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceMarketplace, LegacySingletons: []string{"cutover route remains unavailable until canonical product delegation lands"}, HookStatus: StrideE10TenantHookPending},
		{Surface: StrideE10TenantSurfaceCache, LegacySingletons: []string{"full authority key has no production cache consumer"}, HookStatus: StrideE10TenantHookPending},
	}
}

func strideE10SessionHashFromRequest(request *http.Request) string {
	if request == nil {
		return ""
	}
	token := sessionTokenFromRequest(request)
	if token == "" {
		return ""
	}
	return hashResetToken(token)
}

// withStrideE10TenantRequestUse holds the converter's current-authority
// callback through the final body read, response projection, or mutation. Off
// and shadow call the exact same legacy closure. The principal is carried only
// in memory and never accepted from a client body or query.
func withStrideE10TenantRequestUse(request *http.Request, surface StrideE10TenantSurface, use func(context.Context, *StrideE10TenantPrincipal) error) error {
	if request == nil || use == nil {
		return ErrStrideE10TenantAuthorityInvalid
	}
	ctx := request.Context()
	return withStrideE10TenantRuntimeAuthority(ctx, surface, strideE10SessionHashFromRequest(request),
		func() error {
			bound := context.WithValue(ctx, strideE10TenantSurfaceContextKey{}, surface)
			return use(bound, nil)
		},
		func(principal StrideE10TenantPrincipal) error {
			bound := principal
			canonical := context.WithValue(ctx, strideE10TenantSurfaceContextKey{}, surface)
			canonical, release, err := strideE10BindCurrentHeldTenantAuthority(canonical, currentStrideE10TenantRuntimeConverter(), bound, strideE10SessionHashFromRequest(request), surface)
			if err != nil {
				return err
			}
			defer release()
			return use(canonical, &bound)
		})
}

func strideE10TenantSurfaceUseBound(ctx context.Context, surface StrideE10TenantSurface) bool {
	if ctx == nil {
		return false
	}
	bound, ok := ctx.Value(strideE10TenantSurfaceContextKey{}).(StrideE10TenantSurface)
	return ok && bound == surface
}

func strideE10TenantPrincipalFromContext(ctx context.Context) (StrideE10TenantPrincipal, bool) {
	if ctx == nil {
		return StrideE10TenantPrincipal{}, false
	}
	principal, ok := ctx.Value(strideE10TenantPrincipalContextKey{}).(StrideE10TenantPrincipal)
	return principal, ok && principal.TenantID != "" && principal.PersonID != ""
}

func strideE10TenantCutoverEnabled() bool {
	converter := currentStrideE10TenantRuntimeConverter()
	return converter != nil && converter.gate != nil && converter.gate.Enabled() && converter.mode == StrideE10TenantConversionCutover
}

func writeStrideE10TenantHookError(writer http.ResponseWriter, err error, unavailable string) {
	if errors.Is(err, ErrStrideE10TenantAuthorityInvalid) || errors.Is(err, ErrStrideE10TenantAuthorityStale) || errors.Is(err, ErrStrideE10TenantConversionDisabled) {
		writeAuthError(writer, http.StatusServiceUnavailable, unavailable)
		return
	}
	writeAuthError(writer, http.StatusInternalServerError, unavailable)
}
