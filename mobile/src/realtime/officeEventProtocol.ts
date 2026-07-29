export type OfficeEventEnvelope = {
  event: string;
  data: unknown;
};

export function parseOfficeEventEnvelope(raw: unknown): OfficeEventEnvelope | null {
  if (typeof raw !== 'string') return null;
  try {
    const outer = JSON.parse(raw) as { event?: unknown; data?: unknown };
    if (outer.event !== 'kanban' || typeof outer.data !== 'string') return null;
    const nested = JSON.parse(outer.data) as { event?: unknown; data?: unknown };
    const event = typeof nested.event === 'string' ? nested.event.trim() : '';
    return event ? { event, data: nested.data } : null;
  } catch {
    return null;
  }
}

export function encodeOfficeCommand(event: string, data: unknown): string | null {
  const normalized = event.trim();
  if (!normalized) return null;
  try {
    return JSON.stringify({ event: normalized, data: JSON.stringify(data ?? {}) });
  } catch {
    return null;
  }
}
