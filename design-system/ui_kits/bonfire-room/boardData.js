export const STATUSES = ["Backlog", "In Progress", "Blocked", "Done"];

// Tag taxonomy matches production.
export const TAG_TAXONOMY = {
  protocol: new Set(["webrtc", "rtp", "srtp", "dtls", "ice", "nack", "sdp", "turn", "stun"]),
  concern: new Set(["risk", "bandwidth", "signaling", "security", "latency", "blocked", "auth"]),
  mechanism: new Set(["simulcast", "hevc", "opus", "h264", "vp8", "vp9", "rtcp", "fec"]),
};

export function tagKind(tag) {
  const t = String(tag || "").toLowerCase().trim();
  if (TAG_TAXONOMY.protocol.has(t)) return "protocol";
  if (TAG_TAXONOMY.concern.has(t)) return "concern";
  if (TAG_TAXONOMY.mechanism.has(t)) return "mechanism";
  return "mechanism";
}

// Fallback hash-driven warm chip color for unknown tags.
export function tagColors(tag) {
  let hash = 0;
  for (const c of String(tag)) hash = ((hash << 5) - hash + c.charCodeAt(0)) | 0;
  const hue = 12 + (Math.abs(hash) % 46);
  return { background: `hsl(${hue} 78% 91%)`, color: `hsl(${hue} 70% 28%)` };
}
