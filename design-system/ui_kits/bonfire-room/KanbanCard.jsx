import { OwnerAvatar } from "./OwnerAvatar.jsx";
import { TagList } from "./TagList.jsx";

export function KanbanCard({ card, moved = false, onOpen }) {
  return (
    <button
      className={`card${moved ? " moved" : ""}`}
      type="button"
      aria-label={`Open card details for ${card.title || "untitled card"}`}
      onClick={() => onOpen?.(card)}
    >
      <div className="card-heading">
        <strong>{card.title}</strong>
        <OwnerAvatar name={card.owner || "Unassigned"} />
      </div>
      <div className="card-meta">owner · {card.owner || "Unassigned"}</div>
      <TagList tags={card.tags} />
    </button>
  );
}
