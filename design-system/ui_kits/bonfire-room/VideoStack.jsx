// VideoStack.jsx — local + remote tiles. No real WebRTC; placeholders only.
function VideoTile({ name = "you", speaking = false, isLocal = false }) {
  return (
    <div className={`video-tile${speaking ? " is-speaking" : ""}`}>
      <div className="video-tile__placeholder">
        {name && name !== "you" ? (
          <span className="video-tile__avatar">
            <OwnerAvatar name={name} large />
          </span>
        ) : (
          <span>{isLocal ? "your camera" : "no video"}</span>
        )}
      </div>
      <span className="video-label">{name}</span>
    </div>
  );
}

function VideoStack({ localName = "you", participants = [] }) {
  return (
    <div className="video-stack mount-stagger">
      <VideoTile name={localName} isLocal />
      {participants.map((p) => (
        <VideoTile key={p.name} name={p.name} speaking={p.speaking} />
      ))}
    </div>
  );
}

Object.assign(window, { VideoTile, VideoStack });
