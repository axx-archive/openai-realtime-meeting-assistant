export type PushRegistrationAuthority = {
  accountKey: string;
  sessionToken: string;
  token: string | null;
  pendingRevocation: boolean;
  deviceCleared: boolean;
};

type SerializedLedger = {
  version: 1;
  authorities: PushRegistrationAuthority[];
};

function normalizedAccount(value: string): string {
  return value.trim().toLowerCase();
}

function normalizedToken(value: string | null | undefined): string | null {
  const normalized = value?.trim() ?? '';
  return normalized || null;
}

function authorityID(authority: PushRegistrationAuthority): string {
  return `${authority.accountKey}\u0000${authority.sessionToken}\u0000${authority.token ?? ''}`;
}

function parseAuthority(value: unknown): PushRegistrationAuthority | null {
  if (typeof value !== 'object' || value === null) return null;
  const record = value as Record<string, unknown>;
  const accountKey = typeof record.accountKey === 'string'
    ? normalizedAccount(record.accountKey)
    : '';
  const sessionToken = typeof record.sessionToken === 'string'
    ? record.sessionToken.trim()
    : '';
  const token = typeof record.token === 'string' ? normalizedToken(record.token) : null;
  if (!accountKey || !sessionToken) return null;
  return {
    accountKey,
    sessionToken,
    token,
    pendingRevocation: record.pendingRevocation === true,
    deviceCleared: record.pendingRevocation === true && record.deviceCleared === true,
  };
}

/**
 * Process authority for Expo bindings. It deliberately retains the captured
 * old session: an offline sign-out must be able to unregister later instead
 * of forgetting the only credential that can remove that account's binding.
 */
export class PushAuthorityLedger {
  private readonly authorities = new Map<string, PushRegistrationAuthority>();

  private readonly registrations = new Map<string, Set<Promise<unknown>>>();

  hydrate(raw: string | null): void {
    if (!raw) return;
    try {
      const parsed = JSON.parse(raw) as Partial<SerializedLedger>;
      if (parsed.version !== 1 || !Array.isArray(parsed.authorities)) return;
      for (const value of parsed.authorities) {
        const authority = parseAuthority(value);
        if (authority) this.authorities.set(authorityID(authority), authority);
      }
    } catch {
      // A malformed ledger is ignored; the legacy token remains available.
    }
  }

  serialize(): string | null {
    const authorities = this.snapshot();
    if (!authorities.length) return null;
    return JSON.stringify({ version: 1, authorities } satisfies SerializedLedger);
  }

  snapshot(): PushRegistrationAuthority[] {
    return [...this.authorities.values()].map((authority) => ({ ...authority }));
  }

  remember(
    accountKey: string,
    sessionToken: string,
    token: string | null,
    pendingRevocation = false,
    deviceCleared = false,
  ): PushRegistrationAuthority | null {
    const authority = parseAuthority({
      accountKey,
      sessionToken,
      token,
      pendingRevocation,
      deviceCleared,
    });
    if (!authority) return null;
    this.authorities.set(authorityID(authority), authority);
    return { ...authority };
  }

  registrationSucceeded(authority: PushRegistrationAuthority): PushRegistrationAuthority[] {
    const token = normalizedToken(authority.token);
    if (!token) return [];
    // The server keys by Expo token, so a successful upsert proves any prior
    // account binding for this exact token was replaced. Keep its captured
    // session as a pending logout until that second phase succeeds.
    const superseded: PushRegistrationAuthority[] = [];
    const nextID = authorityID({ ...authority, token });
    for (const current of this.snapshot()) {
      if (current.token !== token || authorityID(current) === nextID) continue;
      this.authorities.delete(authorityID(current));
      const pending = {
        ...current,
        pendingRevocation: true,
        deviceCleared: true,
      };
      this.authorities.set(authorityID(pending), pending);
      superseded.push({ ...pending });
    }
    this.remember(authority.accountKey, authority.sessionToken, token, false, false);
    return superseded;
  }

  markSessionPending(
    accountKey: string,
    sessionToken: string,
  ): PushRegistrationAuthority[] {
    const normalized = normalizedAccount(accountKey);
    const matches: PushRegistrationAuthority[] = [];
    for (const current of this.snapshot()) {
      if (current.accountKey !== normalized || current.sessionToken !== sessionToken) continue;
      this.authorities.delete(authorityID(current));
      const pending = { ...current, pendingRevocation: true };
      this.authorities.set(authorityID(pending), pending);
      matches.push({ ...pending });
    }
    return matches;
  }

  attachTokenToPendingSession(
    accountKey: string,
    sessionToken: string,
    token: string,
  ): PushRegistrationAuthority | null {
    const normalized = normalizedAccount(accountKey);
    const candidate = [...this.authorities.entries()].find(([, authority]) => (
      authority.accountKey === normalized
      && authority.sessionToken === sessionToken
      && authority.pendingRevocation
      && !authority.token
    ));
    if (!candidate) return null;
    this.authorities.delete(candidate[0]);
    return this.remember(normalized, sessionToken, token, true, false);
  }

  pending(): PushRegistrationAuthority[] {
    return this.snapshot().filter((authority) => authority.pendingRevocation);
  }

  forSession(accountKey: string, sessionToken: string): PushRegistrationAuthority[] {
    const normalized = normalizedAccount(accountKey);
    return this.snapshot().filter((authority) => (
      authority.accountKey === normalized && authority.sessionToken === sessionToken
    ));
  }

  remove(authority: PushRegistrationAuthority): void {
    this.authorities.delete(authorityID(authority));
  }

  markDeviceCleared(authority: PushRegistrationAuthority): PushRegistrationAuthority {
    this.authorities.delete(authorityID(authority));
    const cleared = {
      ...authority,
      pendingRevocation: true,
      deviceCleared: true,
    };
    this.authorities.set(authorityID(cleared), cleared);
    return { ...cleared };
  }

  removeSession(accountKey: string, sessionToken: string): void {
    const normalized = normalizedAccount(accountKey);
    for (const [id, authority] of this.authorities) {
      if (authority.accountKey === normalized && authority.sessionToken === sessionToken) {
        this.authorities.delete(id);
      }
    }
  }

  removeAccountToken(accountKey: string, token: string): void {
    const normalized = normalizedAccount(accountKey);
    const deviceToken = normalizedToken(token);
    if (!normalized || !deviceToken) return;
    for (const [id, authority] of this.authorities) {
      if (authority.accountKey === normalized && authority.token === deviceToken) {
        this.authorities.delete(id);
      }
    }
  }

  trackRegistration(
    authority: PushRegistrationAuthority,
    work: Promise<unknown>,
  ): Promise<unknown> {
    const id = authorityID(authority);
    let active = this.registrations.get(id);
    if (!active) {
      active = new Set();
      this.registrations.set(id, active);
    }
    active.add(work);
    void work.finally(() => {
      active?.delete(work);
      if (!active?.size) this.registrations.delete(id);
    }).catch(() => undefined);
    return work;
  }

  registrationsForSession(accountKey: string, sessionToken: string): Promise<unknown>[] {
    const normalized = normalizedAccount(accountKey);
    const result: Promise<unknown>[] = [];
    for (const [id, active] of this.registrations) {
      const [authorityAccount, authoritySession] = id.split('\u0000');
      if (authorityAccount === normalized && authoritySession === sessionToken) {
        result.push(...active);
      }
    }
    return result;
  }
}

export const pushAuthorityLedger = new PushAuthorityLedger();
