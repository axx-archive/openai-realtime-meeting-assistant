import { OwnerAvatar } from "./OwnerAvatar.jsx";

// VideoTile.jsx — local or remote placeholder video.
export function VideoTile({ name = "you", speaking = false, isLocal = false }) {
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
