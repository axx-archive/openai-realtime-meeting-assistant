export type NativeClientConfig = {
  rtcConfiguration: { iceServers?: Array<Record<string, unknown>> };
  websocketPath?: string;
  supportedLayers?: string[];
  privateRealtimeVoiceQualified?: boolean;
};

type ClientConfigLoader = (sessionToken: string) => Promise<NativeClientConfig>;

const PERSONAL_REALTIME_CONFIG_MAX_AGE_MS = 15_000;

/**
 * One session-scoped transport for native client configuration. Room entry and
 * personal Realtime can ask for the same server projection without multiplying
 * concurrent requests, while an account switch immediately fences late data.
 */
export class NativeClientConfigCache {
  private authorityToken = '';
  private generation = 0;
  private cached: { value: NativeClientConfig; receivedAt: number } | null = null;
  private inFlight: Promise<NativeClientConfig> | null = null;

  constructor(
    private readonly loader: ClientConfigLoader,
    private readonly now: () => number = Date.now,
  ) {}

  load(
    sessionToken: string,
    options: { force?: boolean; maxAgeMs?: number } = {},
  ): Promise<NativeClientConfig> {
    const authorityToken = sessionToken.trim();
    if (!authorityToken) return Promise.reject(new Error('Native client configuration requires a session.'));
    if (this.authorityToken !== authorityToken) {
      this.authorityToken = authorityToken;
      this.generation += 1;
      this.cached = null;
      this.inFlight = null;
    }
    const maxAgeMs = Math.max(0, options.maxAgeMs ?? PERSONAL_REALTIME_CONFIG_MAX_AGE_MS);
    if (
      !options.force
      && this.cached
      && this.now() - this.cached.receivedAt <= maxAgeMs
    ) return Promise.resolve(this.cached.value);
    if (this.inFlight) return this.inFlight;

    const generation = this.generation;
    let request: Promise<NativeClientConfig>;
    request = Promise.resolve()
      .then(() => this.loader(authorityToken))
      .then((value) => {
        if (this.authorityToken === authorityToken && this.generation === generation) {
          this.cached = { value, receivedAt: this.now() };
        }
        return value;
      })
      .finally(() => {
        if (this.inFlight === request) this.inFlight = null;
      });
    this.inFlight = request;
    return request;
  }

  clear(sessionToken?: string): void {
    if (sessionToken && this.authorityToken !== sessionToken.trim()) return;
    this.authorityToken = '';
    this.generation += 1;
    this.cached = null;
    this.inFlight = null;
  }
}

export function privateRealtimeVoiceIsQualified(config: NativeClientConfig): boolean {
  return config.privateRealtimeVoiceQualified === true;
}
