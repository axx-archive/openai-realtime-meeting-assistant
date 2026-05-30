import { identiconCells } from "./identicon.js";

// OwnerAvatar — circular wrapper around an identicon.
export function OwnerAvatar({ name = "Unassigned", large = false }) {
  const { cells, color } = identiconCells(name);
  return (
    <span className={`owner-avatar${large ? " large" : ""}`} title={`owner: ${name}`} aria-label={`Owner: ${name}`}>
      <svg viewBox="0 0 5 5" aria-hidden="true">
        <rect width="5" height="5" fill="#F6F8FA" />
        {cells.map(([x, y], i) => (
          <rect key={i} x={x} y={y} width="1" height="1" fill={color} />
        ))}
      </svg>
    </span>
  );
}
