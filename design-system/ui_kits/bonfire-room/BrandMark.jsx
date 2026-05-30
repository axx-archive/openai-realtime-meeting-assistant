// BrandMark.jsx — the logo with three halos.
// Toggle `listening` to start the 2400ms pulse cycle.
// Toggle `hotEmber` for the 1.6s recognition handshake (faster + drop-shadow).

export function BrandMark({ listening = false, hotEmber = false, size = 36 }) {
  const cls = `topbar__mark${listening ? " is-listening" : ""}${hotEmber ? " is-hot-ember" : ""}`;
  return (
    <span className={cls} aria-hidden="true" style={{ width: size, height: size }}>
      <span className="listening-halo listening-halo--3"></span>
      <span className="listening-halo listening-halo--2"></span>
      <span className="listening-halo listening-halo--1"></span>
      <svg viewBox="0 0 64 64" fill="none">
        <defs>
          <radialGradient id={`emberCore-${size}`} cx="50%" cy="55%" r="55%">
            <stop offset="0%" stopColor="#FFF1E3" />
            <stop offset="22%" stopColor="#FFD27A" />
            <stop offset="55%" stopColor="#FF7A2B" />
            <stop offset="100%" stopColor="#5F1D03" />
          </radialGradient>
          <radialGradient id={`emberHalo-${size}`} cx="50%" cy="55%" r="55%">
            <stop offset="0%" stopColor="#FF7A2B" stopOpacity="0.4" />
            <stop offset="100%" stopColor="#FF7A2B" stopOpacity="0" />
          </radialGradient>
        </defs>
        <rect width="64" height="64" rx="14" fill="#110D09" />
        <circle cx="32" cy="36" r="28" fill={`url(#emberHalo-${size})`} />
        <circle cx="32" cy="36" r="14" fill={`url(#emberCore-${size})`} />
        <ellipse cx="29" cy="31" rx="3" ry="5" fill="#FFF1E3" fillOpacity="0.55" />
      </svg>
    </span>
  );
}
