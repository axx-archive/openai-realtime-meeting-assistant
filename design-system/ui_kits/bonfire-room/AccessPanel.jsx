// AccessPanel.jsx — participant + password form.
import { useState } from "react";

import { PARTICIPANT_NAMES } from "./participants.js";

export function AccessPanel({ verified, onChange, accessState = "locked", accessHint }) {
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");

  const update = (n, p) => {
    setName(n); setPassword(p);
    onChange?.({ name: n, password: p, verified: Boolean(n.trim() && p.trim()) });
  };

  return (
    <section className={`access-panel mount-stagger${verified ? " is-verified" : ""}`} aria-label="Meeting access">
      <header>
        <h2>access</h2>
        <span className="access-state">{accessState}</span>
      </header>
      <div className="access-grid">
        <label className="field">
          <span>participant</span>
          <select value={name} onChange={(e) => update(e.target.value, password)} autoComplete="name">
            <option value="">Select your name</option>
            {PARTICIPANT_NAMES.map((p) => <option key={p}>{p}</option>)}
          </select>
        </label>
        <label className="field">
          <span>password</span>
          <input
            type="password"
            value={password}
            onChange={(e) => update(name, e.target.value)}
            autoComplete="current-password"
            placeholder="Room password"
          />
        </label>
      </div>
      <p className="access-hint">{accessHint}</p>
    </section>
  );
}
