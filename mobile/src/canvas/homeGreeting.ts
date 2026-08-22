export function homePreferredName(value: unknown): string {
  const normalized = String(value ?? '').replace(/\s+/gu, ' ').trim();
  return normalized ? normalized.split(' ')[0] : '';
}

export function personalizedHomeGreeting(value: unknown, now = new Date()): string {
  const hour = now.getHours();
  const salutation = hour < 12 ? 'Good morning' : hour < 17 ? 'Good afternoon' : 'Good evening';
  const name = homePreferredName(value);
  return name ? `${salutation}, ${name}` : salutation;
}
