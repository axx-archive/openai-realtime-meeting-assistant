import { TAG_TAXONOMY, tagColors, tagKind } from "./boardData.js";

const EMPTY_TAGS = Object.freeze([]);

export function TagList({ tags = EMPTY_TAGS }) {
  if (!tags.length) return null;
  return (
    <ul className="tags">
      {tags.map((t) => {
        const kind = tagKind(t);
        const known = kind && TAG_TAXONOMY[kind].has(String(t).toLowerCase());
        const fallbackColors = known ? null : tagColors(t);
        const style = fallbackColors
          ? { "--tag-bg": fallbackColors.background, "--tag-color": fallbackColors.color }
          : null;
        return (
          <li key={t} data-tag-kind={kind} style={style}>{t}</li>
        );
      })}
    </ul>
  );
}
