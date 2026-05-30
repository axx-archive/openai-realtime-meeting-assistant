// Toast.jsx — bottom-right tray.
const EMPTY_TOASTS = Object.freeze([]);

export function ToastTray({ toasts = EMPTY_TOASTS }) {
  return (
    <div className="toast-region" aria-live="polite" aria-atomic="true">
      {toasts.map((t) => (
        <div key={t.id} className={`toast toast--${t.kind || "move"}`}>
          <span className="toast__bar"></span>
          <span>{t.text}</span>
        </div>
      ))}
    </div>
  );
}
