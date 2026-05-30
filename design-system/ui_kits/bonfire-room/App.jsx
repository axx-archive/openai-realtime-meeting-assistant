// App.jsx — wires everything together and runs a scripted meeting.
import { useEffect, useReducer } from "react";
import { createRoot } from "react-dom/client";

import { AccessPanel } from "./AccessPanel.jsx";
import { AssistantPanel } from "./AssistantPanel.jsx";
import { Board } from "./Board.jsx";
import { CardDetail } from "./CardDetail.jsx";
import { MeetingBar } from "./MeetingBar.jsx";
import { MemoryPanel } from "./MemoryPanel.jsx";
import { ToastTray } from "./Toast.jsx";
import { Topbar } from "./Topbar.jsx";
import { VideoStack } from "./VideoStack.jsx";
import { fireSparks } from "./sparks.js";

const INITIAL_CARDS = [
  { id: "card-002", status: "Backlog", title: "Add RTP Retransmission Buffer",   owner: "Tim",     tags: ["webrtc", "rtp", "nack"] },
  { id: "card-003", status: "Backlog", title: "Implement ICE Restart Handling",  owner: "Tyler",   tags: ["webrtc", "ice", "signaling"] },
  { id: "card-004", status: "Backlog", title: "Harden DTLS/SRTP Cleanup",        owner: "Jake",    tags: ["webrtc", "dtls", "srtp"] },
  { id: "card-005", status: "Backlog", title: "Add Simulcast Forwarding Controls", owner: "Caitlyn", tags: ["webrtc", "simulcast", "bandwidth"] },
  { id: "card-001", status: "Backlog", title: "Finish RTP HEVC Packetizer",      owner: "AJ",      tags: ["webrtc", "rtp", "hevc"] },
];

const REMOTE_PARTICIPANTS = [
  { name: "Tim" },
  { name: "Tyler" },
  { name: "Jake" },
];

function nowTime() {
  return new Date().toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}
let _idCounter = 0;
const uid = (p = "id") => `${p}-${Date.now()}-${++_idCounter}`;

const INITIAL_APP_STATE = {
  status: "idle",
  verified: false,
  accessName: "",
  joined: false,
  log: "team meeting",
  accessHint: "Verify access first. Camera and microphone start after the room lets you in.",
  cards: INITIAL_CARDS,
  movedIds: new Set(),
  editing: null,
  boardReady: false,
  participants: [],
  speakingName: null,
  memory: [],
  messages: [],
  latestMessageId: null,
  toasts: [],
  archiveBusy: false,
};

function appReducer(state, action) {
  switch (action.type) {
    case "accessChanged":
      return {
        ...state,
        verified: Boolean(action.verified),
        accessName: action.name || state.accessName,
      };
    case "setEditing":
      return { ...state, editing: action.editing };
    case "pushToast":
      return { ...state, toasts: [...state.toasts, action.toast] };
    case "removeToast":
      return { ...state, toasts: state.toasts.filter((toast) => toast.id !== action.id) };
    case "pushMemory":
      return { ...state, memory: [...state.memory, action.entry].slice(-20) };
    case "pushMessage":
      return {
        ...state,
        messages: [...state.messages, action.message].slice(-40),
        latestMessageId: action.message.id,
      };
    case "startConnecting":
      return {
        ...state,
        log: "checking room access",
        accessHint: `${state.accessName} is ready to verify.`,
        status: "connecting",
      };
    case "roomVerified":
      return {
        ...state,
        accessHint: `${state.accessName} is verified. Starting camera and microphone.`,
        log: `${state.accessName} is verified`,
        status: "room",
      };
    case "roomJoined": {
      const participantNames = [state.accessName, ...REMOTE_PARTICIPANTS.map((p) => p.name)].join(", ");
      return {
        ...state,
        joined: true,
        boardReady: true,
        participants: REMOTE_PARTICIPANTS,
        accessHint: `${state.accessName} is in the room.`,
        log: `in room · ${participantNames}`,
        status: "listening",
      };
    }
    case "setSpeakingName":
      return { ...state, speakingName: action.name };
    case "moveCard":
      return {
        ...state,
        cards: state.cards.map((card) => (card.id === action.id ? { ...card, status: action.toStatus } : card)),
        movedIds: new Set([action.id]),
      };
    case "blockDtlsCard":
      return {
        ...state,
        cards: state.cards.map((card) => (
          card.id === "card-004"
            ? { ...card, status: "Blocked", tags: [...new Set([...(card.tags || []), "blocked"])] }
            : card
        )),
        movedIds: new Set(["card-004"]),
      };
    case "clearMoved":
      return { ...state, movedIds: new Set() };
    case "leaveMeeting":
      return {
        ...state,
        joined: false,
        status: "idle",
        accessHint: INITIAL_APP_STATE.accessHint,
        log: INITIAL_APP_STATE.log,
        participants: [],
        speakingName: null,
      };
    case "setArchiveBusy":
      return { ...state, archiveBusy: action.archiveBusy };
    case "saveCard": {
      const cards = action.isNew
        ? [...state.cards, action.card]
        : state.cards.map((card) => (card.id === action.card.id ? action.card : card));
      return { ...state, cards, editing: null };
    }
    case "deleteCard":
      return {
        ...state,
        cards: state.cards.filter((card) => card.id !== action.card.id),
        editing: null,
      };
    default:
      return state;
  }
}

export function App() {
  const [state, dispatch] = useReducer(appReducer, INITIAL_APP_STATE);
  const {
    status,
    verified,
    accessName,
    joined,
    log,
    accessHint,
    cards,
    movedIds,
    editing,
    boardReady,
    participants,
    speakingName,
    memory,
    messages,
    latestMessageId,
    toasts,
    archiveBusy,
  } = state;

  // Mount stagger toggle.
  useEffect(() => {
    document.body.classList.add("is-mounting");
    const t = setTimeout(() => document.body.classList.remove("is-mounting"), 900);
    return () => clearTimeout(t);
  }, []);

  // Helpers --------------------------------------------------------
  function pushToast(text, kind = "move") {
    const toast = { id: uid("t"), text, kind };
    dispatch({ type: "pushToast", toast });
    setTimeout(() => dispatch({ type: "removeToast", id: toast.id }), 4200);
  }
  function pushMemory(entry) {
    dispatch({ type: "pushMemory", entry: { id: uid("m"), time: nowTime(), ...entry } });
  }
  function pushMessage(entry) {
    dispatch({ type: "pushMessage", message: { id: uid("am"), time: nowTime(), ...entry } });
    if (entry.kind === "error") pushToast(entry.text, "error");
  }
  function moveCard(id, toStatus) {
    dispatch({ type: "moveCard", id, toStatus });
    setTimeout(() => dispatch({ type: "clearMoved" }), 1200);
    const card = cards.find((c) => c.id === id);
    if (card) pushToast(`moved · ${card.title} → ${toStatus}`, "move");
  }

  // Demo timeline --------------------------------------------------
  // When user clicks "Join the room", run a scripted sequence:
  //   verifying → connecting → room → listening
  //   then Tim says he started ICE Restart; card moves to In Progress
  //   then Tyler says he shipped HEVC packetizer; card moves to Done (with sparks)
  //   then Tim is blocked on DTLS; card moves to Blocked
  function startMeeting() {
    if (!verified || joined) return;
    dispatch({ type: "startConnecting" });
    setTimeout(() => {
      dispatch({ type: "roomVerified" });
    }, 700);
    setTimeout(() => {
      dispatch({ type: "roomJoined" });
      pushMessage({ kind: "status", text: "the room is listening." });
    }, 1500);

    // Tim: I started ICE restart handling
    setTimeout(() => {
      dispatch({ type: "setSpeakingName", name: "Tim" });
      pushMemory({ kind: "transcript", text: "Tim: I started the ICE restart handling ticket." });
      pushMessage({ kind: "transcript", text: "Tim: I started the ICE restart handling ticket." });
    }, 3500);
    setTimeout(() => {
      moveCard("card-003", "In Progress");
      pushMessage({ kind: "action", text: "moved Implement ICE Restart Handling → In Progress." });
    }, 4400);
    setTimeout(() => dispatch({ type: "setSpeakingName", name: null }), 5200);

    // Tyler: we shipped HEVC packetizer
    setTimeout(() => {
      dispatch({ type: "setSpeakingName", name: "Tyler" });
      pushMemory({ kind: "transcript", text: "Tyler: We shipped the RTP HEVC packetizer." });
      pushMessage({ kind: "transcript", text: "Tyler: We shipped the RTP HEVC packetizer." });
    }, 7000);
    setTimeout(() => {
      moveCard("card-001", "Done");
      pushMessage({ kind: "action", text: "moved Finish RTP HEVC Packetizer → Done." });
    }, 7900);
    setTimeout(() => dispatch({ type: "setSpeakingName", name: null }), 8800);

    // Jake: blocked on DTLS cleanup
    setTimeout(() => {
      dispatch({ type: "setSpeakingName", name: "Jake" });
      pushMemory({ kind: "transcript", text: "Jake: The DTLS cleanup work is blocked on a transport shutdown issue." });
      pushMessage({ kind: "transcript", text: "Jake: The DTLS cleanup work is blocked on a transport shutdown issue." });
    }, 10500);
    setTimeout(() => {
      dispatch({ type: "blockDtlsCard" });
      setTimeout(() => dispatch({ type: "clearMoved" }), 1200);
      pushToast(`moved · Harden DTLS/SRTP Cleanup → Blocked`, "move");
      pushMessage({ kind: "action", text: "moved Harden DTLS/SRTP Cleanup → Blocked, added blocked tag." });
    }, 11400);
    setTimeout(() => dispatch({ type: "setSpeakingName", name: null }), 12200);
  }

  function leaveMeeting() {
    dispatch({ type: "leaveMeeting" });
  }

  function archiveMeeting() {
    dispatch({ type: "setArchiveBusy", archiveBusy: true });
    pushMessage({ kind: "status", text: "generating meeting notes…" });
    setTimeout(() => {
      pushMemory({ kind: "archive", text: "standup archived · 3 decisions captured" });
      pushMessage({
        kind: "archive",
        text: "meeting archive ready. notes were emailed to participants.",
        downloadUrl: "#archive",
      });
      pushToast("meeting notes emailed", "done");
      dispatch({ type: "setArchiveBusy", archiveBusy: false });
    }, 1100);
  }

  function ask(query) {
    pushMessage({ kind: "query", text: `you asked: ${query}` });
    setTimeout(() => {
      pushMemory({ kind: "answer", text: `answering "${query}" from memory…` });
      pushMessage({ kind: "answer", text: `from the room: Tim started ICE restart handling, Tyler shipped HEVC, Jake is blocked on DTLS cleanup.` });
      pushToast("memory answer ready", "note");
    }, 700);
  }

  const remoteWithSpeaking = participants.map((p) => ({ ...p, speaking: speakingName === p.name }));

  // Status pill mapping. While unjoined we show idle/connecting/offline.
  // While joined we cycle to room → listening.
  const statusLabel = status === "listening" ? "the room is listening"
    : status === "room" ? "room connected"
    : status === "connecting" ? "connecting…"
    : status === "offline" ? "assistant offline"
    : "not connected";

  return (
    <>
      <main>
        <Topbar status={status} />
        <div className="workspace">
          <Board
            cards={cards}
            locked={!joined}
            ready={boardReady}
            movedIds={movedIds}
            onOpen={(card) => dispatch({ type: "setEditing", editing: { card, isNew: false } })}
            onNewCard={() => dispatch({ type: "setEditing", editing: { card: { id: uid("c"), title: "", status: "Backlog", owner: accessName || "Unassigned", tags: [], notes: "" }, isNew: true } })}
          />
          <aside className="video-rail" aria-label="Conference videos">
            <AccessPanel
              verified={verified}
              accessState={joined ? "in room" : verified ? "ready" : "locked"}
              accessHint={accessHint}
              onChange={({ name, verified }) => dispatch({ type: "accessChanged", name, verified })}
            />
            <VideoStack localName={accessName || "you"} participants={remoteWithSpeaking} />
            <MemoryPanel entries={memory} />
            <AssistantPanel
              messages={messages}
              latestId={latestMessageId}
              stateLabel={status === "listening" ? "listening" : "ready"}
              onAsk={ask}
            />
          </aside>
        </div>
        <MeetingBar
          log={log}
          joined={joined}
          canJoin={verified && !joined}
          onJoin={startMeeting}
          onLeave={leaveMeeting}
          onArchive={archiveMeeting}
          archiveBusy={archiveBusy}
        />
      </main>
      {editing && (
        <CardDetail
          key={editing.card.id}
          card={editing.card}
          isNew={editing.isNew}
          onClose={() => dispatch({ type: "setEditing", editing: null })}
          onSave={(next) => {
            dispatch({ type: "saveCard", card: next, isNew: editing.isNew });
            pushToast(editing.isNew ? `new card · ${next.title} → ${next.status}` : `updated · ${next.title}`, "move");
          }}
          onDelete={(c) => {
            dispatch({ type: "deleteCard", card: c });
            pushToast(`deleted · ${c.title}`, "error");
          }}
        />
      )}

      <ToastTray toasts={toasts} />
    </>
  );
}

// Use a tiny effect: when any .card with class "moved" appears AND its column is Done,
// fire sparks anchored to it. (Simpler than threading refs from the App.)
(function installSparksObserver() {
  const observer = new MutationObserver(() => {
    document.querySelectorAll(".board .column").forEach((col) => {
      const h2 = col.querySelector("h2");
      if (h2?.textContent !== "Done") return;
      col.querySelectorAll(".card.moved").forEach((el) => {
        if (el.dataset.sparked) return;
        el.dataset.sparked = "1";
        fireSparks(el);
      });
    });
  });
  if (document.body) observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  else document.addEventListener("DOMContentLoaded", () => observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] }));
})();

const root = createRoot(document.getElementById("root"));
root.render(<App />);
