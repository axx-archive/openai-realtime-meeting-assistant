import { KanbanCard } from "./KanbanCard.jsx";

const EMPTY_CARDS = Object.freeze([]);
const EMPTY_MOVED_IDS = new Set();

export function BoardColumn({ status, cards = EMPTY_CARDS, movedIds = EMPTY_MOVED_IDS, onOpen }) {
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
