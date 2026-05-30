// CardDetail.jsx — modal editor.
import { useEffect, useRef, useState } from "react";

import { STATUSES } from "./boardData.js";
import { OwnerAvatar } from "./OwnerAvatar.jsx";
import { PARTICIPANT_NAMES } from "./participants.js";

const EMPTY_CARD_DRAFT = Object.freeze({ title: "", status: "Backlog", owner: "Unassigned", tags: [], notes: "" });

function normalizeCardDraft(card) {
  return { ...EMPTY_CARD_DRAFT, ...card, tags: card?.tags || EMPTY_CARD_DRAFT.tags };
}

function parseTags(value) {
  return value.split(",").flatMap((tag) => {
    const trimmed = tag.trim();
    return trimmed ? [trimmed] : [];
  });
}

export function CardDetail({ card, isNew = false, onSave, onDelete, onClose }) {
  const dialogRef = useRef(null);
  const [draft, setDraft] = useState(() => normalizeCardDraft(card));

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog || !card) return;
    if (!dialog.open) dialog.showModal();
    return () => {
      if (dialog.open) dialog.close();
    };
  }, [card]);

  if (!card) return null;
  const setField = (k, v) => setDraft((d) => ({ ...d, [k]: v }));
  const title = draft.title.trim();
  const saveCard = () => {
    if (!title) return;
    onSave?.({ ...draft, title, tags: (draft.tags || EMPTY_CARD_DRAFT.tags).flatMap((tag) => (tag ? [tag] : [])) });
  };

  return (
    <dialog ref={dialogRef} className="card-detail-region" aria-label={isNew ? "New card" : `Edit card ${draft.title || "untitled card"}`} onCancel={onClose}>
      <article className="card-detail">
        <header className="card-detail-header">
          <OwnerAvatar name={draft.owner || "Unassigned"} large />
          <div className="card-detail-title">
            <strong>{isNew ? "New card" : draft.title || "Untitled card"}</strong>
            <span className="card-detail-owner">
              {isNew ? "manual project capture" : `owner · ${draft.owner || "Unassigned"}`}
            </span>
          </div>
          <button className="card-detail-close" type="button" aria-label="Close card editor" onClick={onClose}>×</button>
        </header>
        <div
          className="card-detail-form"
        >
          <label className="field">
            <span>title</span>
            <input required value={draft.title} onChange={(e) => setField("title", e.target.value)} />
          </label>
          <div className="card-detail-grid">
            <label className="field">
              <span>status</span>
              <select value={draft.status} onChange={(e) => setField("status", e.target.value)}>
                {STATUSES.map((s) => <option key={s}>{s}</option>)}
              </select>
            </label>
            <label className="field">
              <span>owner</span>
              <select value={draft.owner} onChange={(e) => setField("owner", e.target.value)}>
                {["Unassigned", ...PARTICIPANT_NAMES].map((s) => <option key={s}>{s}</option>)}
              </select>
            </label>
          </div>
          <label className="field">
            <span>tags</span>
            <input
              placeholder="webrtc, risk, bandwidth"
              value={(draft.tags || []).join(", ")}
              onChange={(e) => setField("tags", parseTags(e.target.value))}
            />
          </label>
          <label className="field">
            <span>notes</span>
            <textarea value={draft.notes || ""} onChange={(e) => setField("notes", e.target.value)} />
          </label>
          <div className="card-detail-actions">
            <button className="btn btn--danger" type="button" disabled={isNew} onClick={() => onDelete?.(draft)}>Delete</button>
            <div className="card-detail-actions__right">
              <button className="btn btn--secondary" type="button" onClick={onClose}>Cancel</button>
              <button className="btn btn--primary" type="button" disabled={!title} onClick={saveCard}>{isNew ? "Create card" : "Save changes"}</button>
            </div>
          </div>
        </div>
      </article>
    </dialog>
  );
}
