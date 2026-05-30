// VideoStack.jsx — local + remote tiles. No real WebRTC; placeholders only.
import { VideoTile } from "./VideoTile.jsx";

const EMPTY_PARTICIPANTS = Object.freeze([]);

export function VideoStack({ localName = "you", participants = EMPTY_PARTICIPANTS }) {
  return (
    <div className="video-stack mount-stagger">
      <VideoTile name={localName} isLocal />
      {participants.map((p) => (
        <VideoTile key={p.name} name={p.name} speaking={p.speaking} />
      ))}
    </div>
  );
}
