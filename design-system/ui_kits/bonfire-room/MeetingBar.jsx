// MeetingBar.jsx — sticky footer.
function HangUpIcon() {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M10.68 13.31a16 16 0 0 0 3.41 2.6l1.27-1.27a2 2 0 0 1 2.11-.45 12.84 12.84 0 0 0 2.81.7 2 2 0 0 1 1.72 2v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72c.127.96.361 1.903.7 2.81a2 2 0 0 1-.45 2.11L8.09 9.91" />
      <line x1="22" y1="2" x2="2" y2="22" />
    </svg>
  );
}

export function MeetingBar({ log = "team meeting", joined = false, sharing = false, onJoin, onLeave, onShare, onArchive, archiveBusy = false, canJoin = false }) {
  return (
    <footer className="meeting-bar mount-stagger">
      <p className="log" aria-live="polite" title={log}>{log}</p>
      <div className="controls">
        <button className="btn btn--primary" type="button" disabled={!canJoin || joined} onClick={onJoin}>Join the room</button>
        <button className="btn btn--secondary" type="button" disabled={!joined} onClick={onShare}>{sharing ? "Stop sharing" : "Share screen"}</button>
        <button className="btn btn--secondary" type="button" disabled={!joined || archiveBusy} onClick={onArchive}>{archiveBusy ? "Generating notes" : "Send notes"}</button>
        <button className="btn btn--danger" type="button" aria-label="Leave the room" disabled={!joined} onClick={onLeave}>
          <HangUpIcon />
        </button>
      </div>
      <span aria-hidden="true"></span>
    </footer>
  );
}
