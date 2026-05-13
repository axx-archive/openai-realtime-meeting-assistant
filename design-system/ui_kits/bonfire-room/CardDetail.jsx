// CardDetail.jsx — modal editor.
const { useState: useStateCard, useEffect: useEffectCard } = React;

function CardDetail({ card, isNew = false, onSave, onDelete, onClose }) {
  const [draft, setDraft] = useStateCard(card || { title: "", status: "Backlog", owner: "Unassigned", tags: [], notes: "" });
  useEffectCard(() => { setDraft(card || { title: "", status: "Backlog", owner: "Unassigned", tags: [], notes: "" }); }, [card?.id]);

  if (!card) return null;
  const setField = (k, v) => setDraft((d) => ({ ...d, [k]: v }));

  return (
    <div className="card-detail-region visible" onClick={(e) => { if (e.target === e.currentTarget) onClose?.(); }}>
      <article className="card-detail" role="dialog" aria-modal="true" aria-label={isNew ? "New card" : `Edit card ${draft.title || "untitled card"}`}>
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
        <form
          className="card-detail-form"
          onSubmit={(e) => {
            e.preventDefault();
            onSave?.({ ...draft, tags: (draft.tags || []).filter(Boolean) });
          }}
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
              onChange={(e) => setField("tags", e.target.value.split(",").map((t) => t.trim()).filter(Boolean))}
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
              <button className="btn btn--primary" type="submit">{isNew ? "Create card" : "Save changes"}</button>
            </div>
          </div>
        </form>
      </article>
    </div>
  );
}

Object.assign(window, { CardDetail });
