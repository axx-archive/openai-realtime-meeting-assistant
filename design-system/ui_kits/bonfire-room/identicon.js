// Bonfire identicon — warm-clamped 5×5 mirrored, lifted verbatim from production.
// Returns an SVG DOM node, OR call renderIdenticonReact(seed) for a React element.

function hashString(value) {
  let hash = 2166136261;
  const text = String(value || "");
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

function identiconCells(seed) {
  // Warm clamps: hue 18–60°, sat 60–80%, lit 26–40%.
  const hue = 18 + (hashString(`${seed}:hue`) % 42);
  const sat = 60 + (hashString(`${seed}:sat`) % 20);
  const lit = 26 + (hashString(`${seed}:lit`) % 14);
  const color = `hsl(${hue} ${sat}% ${lit}%)`;

  const patternHash = hashString(`${seed}:pattern`);
  const cells = [];
  let filled = 0;
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 3; x++) {
      const bitIndex = y * 3 + x;
      if (((patternHash >>> bitIndex) & 1) === 0) continue;
      cells.push([x, y]);
      filled++;
      if (x !== 2) cells.push([4 - x, y]);
    }
  }
  if (filled === 0) cells.push([2, 2]);
  return { cells, color };
}

window.hashString = hashString;
window.identiconCells = identiconCells;
