// StatusPill.jsx — five states. Pulse animation is CSS.
const STATUS_LABELS = {
  idle: "not connected",
  connecting: "connecting...",
  room: "room connected",
  listening: "the room is listening",
  offline: "assistant offline",
};

export function StatusPill({ state = "idle", label }) {
  const text = label || STATUS_LABELS[state] || "not connected";
  return (
    <output className={`pill pill--${state}`} aria-live="polite">
      <span className="pill__dot"></span>
      <span>{text}</span>
    </output>
  );
}
