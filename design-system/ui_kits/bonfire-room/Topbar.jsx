import { BrandMark } from "./BrandMark.jsx";
import { StatusPill } from "./StatusPill.jsx";

// Topbar.jsx — mark + title + status.
export function Topbar({ status = "idle" }) {
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
