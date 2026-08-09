import { BonfireApiError, request } from '../api/client';
import {
  parseStrideSurfaceProjection,
  unavailableStrideSurface,
  type StrideSurfaceName,
  type StrideSurfaceProjection,
  type StrideProjectionAction,
  type StrideActionValues,
} from './models';
import { parseStrideActionValues } from './models';
import { buildIdempotencyHeaders } from '../api/requestHelpers';
import { bindStrideSurfaceResource, buildStrideSurfacePath, type StrideSurfaceResourceSelector } from './surfaceSelector';

export type { StrideSurfaceResourceSelector } from './surfaceSelector';

export function requireStrideSession(sessionToken: string | null | undefined): string {
  const token = sessionToken?.trim();
  if (!token) throw new Error('An authenticated session is required.');
  return token;
}

export async function loadStrideSurface(
  sessionToken: string | null | undefined,
  surface: StrideSurfaceName,
  signal?: AbortSignal,
  selector?: StrideSurfaceResourceSelector,
): Promise<StrideSurfaceProjection> {
  const token = requireStrideSession(sessionToken);
  try {
    const payload = await request<unknown>(
      buildStrideSurfacePath(surface, selector),
      { sessionToken: token, signal },
    );
    return bindStrideSurfaceResource(surface, selector, parseStrideSurfaceProjection(surface, payload));
  } catch (error) {
    if (error instanceof BonfireApiError && [403, 404, 501, 503].includes(error.status)) {
      return unavailableStrideSurface(surface);
    }
    throw error;
  }
}

export function createStrideOperationKey(surface: StrideSurfaceName, actionId: string): string {
  return `stride-mobile:${surface}:${actionId}:${Date.now()}:${Math.random().toString(36).slice(2)}`;
}

export async function mutateStrideSurface(
  sessionToken: string | null | undefined,
  surface: StrideSurfaceName,
  action: StrideProjectionAction,
  values: StrideActionValues,
  idempotencyKey: string,
  signal?: AbortSignal,
): Promise<StrideSurfaceProjection> {
  const token = requireStrideSession(sessionToken);
  const operationKey = idempotencyKey.trim();
  if (!operationKey) throw new Error('An idempotency key is required.');
  const closedValues = parseStrideActionValues(action.type, values);
  try {
    const payload = await request<unknown>(
      `/api/stride/v1/mobile/actions/${encodeURIComponent(action.id)}`,
      {
        method: 'POST',
        sessionToken: token,
        signal,
        headers: buildIdempotencyHeaders(operationKey),
        body: {
          action: action.type,
          expectedRevision: action.expectedRevision,
          surface,
          values: closedValues,
        },
      },
    );
    return parseStrideSurfaceProjection(surface, payload);
  } catch (error) {
    if (error instanceof BonfireApiError && [403, 404, 501, 503].includes(error.status)) {
      return unavailableStrideSurface(surface);
    }
    throw error;
  }
}
