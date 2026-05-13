// App.jsx — wires everything together and runs a scripted meeting.
const { useState: useS, useEffect: useE, useRef: useR, useCallback: useC } = React;

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

function App() {
  // Connection state machine
  const [status, setStatus] = useS("idle"); // idle | connecting | room | listening | offline
  const [verified, setVerified] = useS(false);
  const [accessName, setAccessName] = useS("");
  const [joined, setJoined] = useS(false);
  const [log, setLog] = useS("team meeting");
  const [accessHint, setAccessHint] = useS("Verify access first. Camera and microphone start after the room lets you in.");

  // Board
  const [cards, setCards] = useS(INITIAL_CARDS);
  const [movedIds, setMovedIds] = useS(new Set());
  const [editing, setEditing] = useS(null); // { card, isNew }
  const [boardReady, setBoardReady] = useS(false);
  const [participants, setParticipants] = useS([]);
  const [speakingName, setSpeakingName] = useS(null);

  // Memory + assistant
  const [memory, setMemory] = useS([]);
  const [messages, setMessages] = useS([]);
  const [latestMessageId, setLatestMessageId] = useS(null);
  const [toasts, setToasts] = useS([]);
  const [archiveBusy, setArchiveBusy] = useS(false);

  const cardRefsByID = useR({});
  const cardRefAttach = (id) => (el) => { if (el) cardRefsByID.current[id] = el; };

  // Mount stagger toggle.
  useE(() => {
    document.body.classList.add("is-mounting");
    const t = setTimeout(() => document.body.classList.remove("is-mounting"), 900);
    return () => clearTimeout(t);
  }, []);

  // Helpers --------------------------------------------------------
  function pushToast(text, kind = "move") {
    const id = uid("t");
    setToasts((ts) => [...ts, { id, text, kind }]);
    setTimeout(() => setToasts((ts) => ts.filter((t) => t.id !== id)), 4200);
  }
  function pushMemory(entry) {
    setMemory((m) => [...m, { id: uid("m"), time: nowTime(), ...entry }].slice(-20));
  }
  function pushMessage(entry) {
    const id = uid("am");
    setMessages((ms) => [...ms, { id, time: nowTime(), ...entry }].slice(-40));
    setLatestMessageId(id);
    if (entry.kind === "error") pushToast(entry.text, "error");
  }
  function moveCard(id, toStatus) {
    setCards((cs) => cs.map((c) => (c.id === id ? { ...c, status: toStatus } : c)));
    setMovedIds(new Set([id]));
    setTimeout(() => setMovedIds(new Set()), 1200);
    if (toStatus === "Done") {
      setTimeout(() => {
        const ref = cardRefsByID.current[id];
        if (ref) window.fireSparks(ref);
      }, 60);
    }
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
    setLog("checking room access");
    setAccessHint(`${accessName} is ready to verify.`);
    setStatus("connecting");
    setTimeout(() => {
      setAccessHint(`${accessName} is verified. Starting camera and microphone.`);
      setLog(`${accessName} is verified`);
      setStatus("room");
    }, 700);
    setTimeout(() => {
      setJoined(true);
      setBoardReady(true);
      setParticipants(REMOTE_PARTICIPANTS);
      setAccessHint(`${accessName} is in the room.`);
      setLog(`in room · ${[accessName, ...REMOTE_PARTICIPANTS.map((p) => p.name)].join(", ")}`);
      setStatus("listening");
      pushMessage({ kind: "status", text: "the room is listening." });
    }, 1500);

    // Tim: I started ICE restart handling
    setTimeout(() => {
      setSpeakingName("Tim");
      pushMemory({ kind: "transcript", text: "Tim: I started the ICE restart handling ticket." });
      pushMessage({ kind: "transcript", text: "Tim: I started the ICE restart handling ticket." });
    }, 3500);
    setTimeout(() => {
      moveCard("card-003", "In Progress");
      pushMessage({ kind: "action", text: "moved Implement ICE Restart Handling → In Progress." });
    }, 4400);
    setTimeout(() => setSpeakingName(null), 5200);

    // Tyler: we shipped HEVC packetizer
    setTimeout(() => {
      setSpeakingName("Tyler");
      pushMemory({ kind: "transcript", text: "Tyler: We shipped the RTP HEVC packetizer." });
      pushMessage({ kind: "transcript", text: "Tyler: We shipped the RTP HEVC packetizer." });
    }, 7000);
    setTimeout(() => {
      moveCard("card-001", "Done");
      pushMessage({ kind: "action", text: "moved Finish RTP HEVC Packetizer → Done." });
    }, 7900);
    setTimeout(() => setSpeakingName(null), 8800);

    // Jake: blocked on DTLS cleanup
    setTimeout(() => {
      setSpeakingName("Jake");
      pushMemory({ kind: "transcript", text: "Jake: The DTLS cleanup work is blocked on a transport shutdown issue." });
      pushMessage({ kind: "transcript", text: "Jake: The DTLS cleanup work is blocked on a transport shutdown issue." });
    }, 10500);
    setTimeout(() => {
      setCards((cs) => cs.map((c) => (c.id === "card-004" ? { ...c, status: "Blocked", tags: [...new Set([...(c.tags||[]), "blocked"])] } : c)));
      setMovedIds(new Set(["card-004"]));
      setTimeout(() => setMovedIds(new Set()), 1200);
      pushToast(`moved · Harden DTLS/SRTP Cleanup → Blocked`, "move");
      pushMessage({ kind: "action", text: "moved Harden DTLS/SRTP Cleanup → Blocked, added blocked tag." });
    }, 11400);
    setTimeout(() => setSpeakingName(null), 12200);
  }

  function leaveMeeting() {
    setJoined(false);
    setStatus("idle");
    setAccessHint("Verify access first. Camera and microphone start after the room lets you in.");
    setLog("team meeting");
    setParticipants([]);
    setSpeakingName(null);
  }

  function archiveMeeting() {
    setArchiveBusy(true);
    pushMessage({ kind: "status", text: "generating meeting notes…" });
    setTimeout(() => {
      pushMemory({ kind: "archive", text: "standup archived · 3 decisions captured" });
      pushMessage({
        kind: "archive",
        text: "meeting archive ready. notes were emailed to participants.",
        downloadUrl: "#archive",
      });
      pushToast("meeting notes emailed", "done");
      setArchiveBusy(false);
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
    <React.Fragment>
      <main>
        <Topbar status={status} />
        <div className="workspace">
          <Board
            cards={cards}
            locked={!joined}
            ready={boardReady}
            movedIds={movedIds}
            onOpen={(card) => setEditing({ card, isNew: false })}
            onNewCard={() => setEditing({ card: { id: uid("c"), title: "", status: "Backlog", owner: accessName || "Unassigned", tags: [], notes: "" }, isNew: true })}
          />
          <aside className="video-rail" aria-label="Conference videos">
            <AccessPanel
              verified={verified}
              accessState={joined ? "in room" : verified ? "ready" : "locked"}
              accessHint={accessHint}
              onChange={({ name, verified }) => { setVerified(!!verified); if (name) setAccessName(name); }}
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

      {/* Render the live card refs through a portal-free side-channel so sparks have an anchor. */}
      <div aria-hidden="true" style={{ position: "fixed", inset: 0, pointerEvents: "none" }}>
        {/* Hook ref attachers into KanbanCard via DOM lookup at runtime — simpler than threading refs. */}
      </div>

      {editing && (
        <CardDetail
          card={editing.card}
          isNew={editing.isNew}
          onClose={() => setEditing(null)}
          onSave={(next) => {
            if (editing.isNew) {
              setCards((cs) => [...cs, next]);
              pushToast(`new card · ${next.title} → ${next.status}`, "move");
            } else {
              setCards((cs) => cs.map((c) => (c.id === next.id ? next : c)));
              pushToast(`updated · ${next.title}`, "move");
            }
            setEditing(null);
          }}
          onDelete={(c) => {
            setCards((cs) => cs.filter((x) => x.id !== c.id));
            pushToast(`deleted · ${c.title}`, "error");
            setEditing(null);
          }}
        />
      )}

      <ToastTray toasts={toasts} />
    </React.Fragment>
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
        window.fireSparks(el);
      });
    });
  });
  if (document.body) observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] });
  else document.addEventListener("DOMContentLoaded", () => observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["class"] }));
})();

const root = ReactDOM.createRoot(document.getElementById("root"));
root.render(<App />);
