// Board.jsx — parchment kanban + KanbanCard + Tag.
import { STATUSES } from "./boardData.js";
import { BoardColumn } from "./BoardColumn.jsx";

const EMPTY_CARDS = Object.freeze([]);
const EMPTY_MOVED_IDS = new Set();

export function Board({ cards = EMPTY_CARDS, locked = false, ready = true, movedIds = EMPTY_MOVED_IDS, onOpen, statusLabel, onNewCard, onUndoDelete, canUndo = false }) {
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
