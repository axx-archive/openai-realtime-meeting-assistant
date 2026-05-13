// Board.jsx — parchment kanban + KanbanCard + Tag.
const STATUSES = ["Backlog", "In Progress", "Blocked", "Done"];

// Tag taxonomy matches production.
const TAG_TAXONOMY = {
  protocol: new Set(["webrtc","rtp","srtp","dtls","ice","nack","sdp","turn","stun"]),
  concern:  new Set(["risk","bandwidth","signaling","security","latency","blocked","auth"]),
  mechanism:new Set(["simulcast","hevc","opus","h264","vp8","vp9","rtcp","fec"]),
};
function tagKind(tag) {
  const t = String(tag || "").toLowerCase().trim();
  if (TAG_TAXONOMY.protocol.has(t)) return "protocol";
  if (TAG_TAXONOMY.concern.has(t)) return "concern";
  if (TAG_TAXONOMY.mechanism.has(t)) return "mechanism";
  return "mechanism";
}
// Fallback hash-driven warm chip color for unknown tags.
function tagColors(tag) {
  let hash = 0;
  for (const c of String(tag)) hash = ((hash << 5) - hash + c.charCodeAt(0)) | 0;
  const hue = 12 + (Math.abs(hash) % 46);
  return { background: `hsl(${hue} 78% 91%)`, color: `hsl(${hue} 70% 28%)` };
}

function TagList({ tags = [] }) {
  if (!tags.length) return null;
  return (
    <ul className="tags">
      {tags.map((t) => {
        const kind = tagKind(t);
        const known = kind && TAG_TAXONOMY[kind].has(String(t).toLowerCase());
        const style = known ? null : { "--tag-bg": tagColors(t).background, "--tag-color": tagColors(t).color };
        return (
          <li key={t} data-tag-kind={kind} style={style}>{t}</li>
        );
      })}
    </ul>
  );
}

function KanbanCard({ card, moved = false, onOpen }) {
  return (
    <article
      className={`card${moved ? " moved" : ""}`}
      tabIndex={0}
      role="button"
      aria-label={`Open card details for ${card.title || "untitled card"}`}
      onClick={() => onOpen?.(card)}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpen?.(card); } }}
    >
      <div className="card-heading">
        <strong>{card.title}</strong>
        <OwnerAvatar name={card.owner || "Unassigned"} />
      </div>
      <div className="card-meta">owner · {card.owner || "Unassigned"}</div>
      <TagList tags={card.tags} />
    </article>
  );
}

function BoardColumn({ status, cards, movedIds, onOpen }) {
  return (
    <section className="column" aria-label={`${status} cards`}>
      <header>
        <h2>{status}</h2>
        <span className="count">{cards.length}</span>
      </header>
      <div className="stack">
        {cards.length === 0 ? (
          <div className="empty">nothing here yet</div>
        ) : (
          cards.map((c) => (
            <KanbanCard key={c.id} card={c} moved={movedIds.has(c.id)} onOpen={onOpen} />
          ))
        )}
      </div>
    </section>
  );
}

function Board({ cards = [], locked = false, ready = true, movedIds = new Set(), onOpen, statusLabel, onNewCard, onUndoDelete, canUndo = false }) {
  const empty = cards.length === 0;
  return (
    <section className="presentation-tile" aria-label="Shared workspace">
      <div className={`board-surface mount-stagger${locked ? " is-locked" : ""}${empty && !locked ? " is-empty" : ""}`}>
        <div className="board-toolbar" aria-label="Board tools">
          <span className="board-status">{statusLabel || (ready ? `${cards.length} project${cards.length === 1 ? "" : "s"}` : "waiting room")}</span>
          <div className="board-tools">
            <button className="btn btn--secondary" type="button" disabled={locked} onClick={onNewCard}>New card</button>
            <button className="btn btn--secondary" type="button" disabled={locked || !canUndo} onClick={onUndoDelete}>Undo delete</button>
          </div>
        </div>
        <p className="board-hero" aria-hidden="true">join the room to load the board.</p>
        <section className="board" aria-label="Kanban board">
          {STATUSES.map((s) => (
            <BoardColumn
              key={s}
              status={s}
              cards={cards.filter((c) => c.status === s)}
              movedIds={movedIds}
              onOpen={onOpen}
            />
          ))}
        </section>
      </div>
    </section>
  );
}

Object.assign(window, { STATUSES, Board, BoardColumn, KanbanCard, TagList, tagKind, tagColors });
