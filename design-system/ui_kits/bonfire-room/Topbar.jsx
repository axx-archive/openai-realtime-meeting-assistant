// StatusPill.jsx — five states. Pulse animation is CSS.
const STATUS_LABELS = {
  idle: "not connected",
  connecting: "connecting…",
  room: "room connected",
  listening: "the room is listening",
  offline: "assistant offline",
};

function StatusPill({ state = "idle", label }) {
  const text = label || STATUS_LABELS[state] || "not connected";
  return (
    <span className={`pill pill--${state}`} role="status" aria-live="polite">
      <span className="pill__dot"></span>
      <span>{text}</span>
    </span>
  );
}

// Topbar.jsx — mark + title + status.
function Topbar({ status = "idle" }) {
  const listening = status === "listening";
  return (
    <header className="topbar mount-stagger">
      <BrandMark listening={listening} />
      <div className="topbar__title">
        <h1>The Bonfire</h1>
        <p>agentic meeting room</p>
      </div>
      <StatusPill state={status} />
    </header>
  );
}

Object.assign(window, { StatusPill, Topbar, STATUS_LABELS });
