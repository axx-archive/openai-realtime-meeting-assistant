export type UnauthorizedHandler = (requestSessionToken: string | null) => void;

let unauthorizedHandler: UnauthorizedHandler | null = null;

export function setUnauthorizedHandler(handler: UnauthorizedHandler | null): void {
  unauthorizedHandler = handler;
}

/** Called immediately when response headers establish revocation. */
export function fenceUnauthorizedResponse(
  status: number,
  requestSessionToken: string | null | undefined,
  suppress = false,
): void {
  if (status === 401 && !suppress) {
    unauthorizedHandler?.(requestSessionToken ?? null);
  }
}

/** Testable ordering primitive: the fence runs before body I/O can suspend. */
export async function readTextAfterUnauthorizedFence(
  response: Pick<Response, 'status' | 'text'>,
  requestSessionToken: string | null | undefined,
  suppress = false,
): Promise<string> {
  fenceUnauthorizedResponse(response.status, requestSessionToken, suppress);
  return response.text();
}
