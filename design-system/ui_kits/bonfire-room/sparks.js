// Sparks — fires from a DOM node (the just-completed card).
const SPARK_PALETTE = ["#FF7A2B", "#FFB23F", "#FFD27A", "#FFA463", "#ED5A14", "#FFF1E3", "#FFC082"];
const SPARK_COUNT = 22;
let lastSparksAt = 0;

export function fireSparks(anchor) {
  if (!anchor) return;
  const now = performance.now();
  if (now - lastSparksAt < 6000) return;
  lastSparksAt = now;
  const rect = anchor.getBoundingClientRect();
  const ox = rect.left + rect.width / 2;
  const oy = rect.top + Math.min(rect.height / 2, 48);
  const lifetime = 1000;
  const gravity = 480;

  for (let i = 0; i < SPARK_COUNT; i++) {
    const vy = -180 - Math.random() * 80;
    const vx = (Math.random() - 0.5) * 180;
    const t = lifetime / 1000;
    const dx = vx * t;
    const dy = vy * t + 0.5 * gravity * t * t;
    const rotation = (Math.random() - 0.5) * 720;
    const delay = Math.random() * 600;
    const piece = document.createElement("span");
    piece.className = "spark";
    piece.style.cssText = [
      `left: ${ox}px`,
      `top: ${oy}px`,
      `--dx: ${dx}px`,
      `--dy: ${dy}px`,
      `--rotation: ${rotation}deg`,
      `--spark-color: ${SPARK_PALETTE[i % SPARK_PALETTE.length]}`,
      `animation-delay: ${delay}ms`,
    ].join(";");
    document.body.appendChild(piece);
    setTimeout(() => piece.remove(), lifetime + delay + 50);
  }
}
