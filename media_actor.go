package main

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// roomMediaCommand is the bounded command vocabulary for the actorized Pion
// control plane. Media packets never cross this mailbox: only lifecycle and
// reconciliation requests do. Commands are represented as sticky bits, so a
// burst is coalesced without losing the fact that required reconciliation is
// pending and the mailbox can never grow without bound.
type roomMediaCommand uint32

const (
	roomMediaCommandAdmit roomMediaCommand = 1 << iota
	roomMediaCommandLeave
	roomMediaCommandTrack
	roomMediaCommandSignal
	roomMediaCommandRestart
	roomMediaCommandClose
)

type roomMediaActor struct {
	roomID     string
	generation uint64

	enqueueMu sync.Mutex
	pending   atomic.Uint32
	accepted  atomic.Uint64
	wake      chan struct{}
	done      chan struct{}
	closing   atomic.Bool
	closed    atomic.Bool

	handler func(string, roomMediaCommand)
}

var roomMediaActors = struct {
	sync.Mutex
	actors map[string]*roomMediaActor
}{actors: map[string]*roomMediaActor{}}

func newRoomMediaActor(roomID string, generation uint64, handler func(string, roomMediaCommand)) *roomMediaActor {
	actor := &roomMediaActor{
		roomID:     normalizeRoomID(roomID),
		generation: generation,
		wake:       make(chan struct{}, 1),
		done:       make(chan struct{}),
		handler:    handler,
	}
	go actor.run()
	return actor
}

func (actor *roomMediaActor) enqueue(command roomMediaCommand) bool {
	if actor == nil || command == 0 {
		return false
	}
	// Serialize the acceptance decision with the close transition. Without this
	// lock a producer could observe closing=false, lose the CPU to close(), then
	// publish work behind the terminal close bit while still returning true.
	actor.enqueueMu.Lock()
	defer actor.enqueueMu.Unlock()
	if actor.closed.Load() {
		return false
	}
	if command&roomMediaCommandClose != 0 {
		actor.closing.Store(true)
	} else if actor.closing.Load() {
		return false
	}
	for {
		current := actor.pending.Load()
		if actor.pending.CompareAndSwap(current, current|uint32(command)) {
			break
		}
	}
	actor.accepted.Add(1)
	select {
	case actor.wake <- struct{}{}:
	default:
	}
	return true
}

func roomMediaActorAcceptedCommands(roomID string) uint64 {
	roomID = normalizeRoomID(roomID)
	roomMediaActors.Lock()
	actor := roomMediaActors.actors[roomID]
	roomMediaActors.Unlock()
	if actor == nil {
		return 0
	}
	return actor.accepted.Load()
}

func (actor *roomMediaActor) run() {
	defer close(actor.done)
	for range actor.wake {
		// Coalesce signaling bursts before touching Pion. Close is deliberately
		// not delayed: it is a sticky, terminal command and must fence all later
		// callbacks immediately.
		if roomMediaCommand(actor.pending.Load())&roomMediaCommandClose == 0 {
			time.Sleep(peerSignalDebounce)
		}
		commands := roomMediaCommand(actor.pending.Swap(0))
		if commands == 0 {
			continue
		}
		terminal := commands&roomMediaCommandClose != 0
		work := commands &^ roomMediaCommandClose
		if terminal {
			// Admission, leave, and track/state reconciliation are required work.
			// They may already be executing, or may have coalesced with close; in
			// both cases the single actor drains them before the terminal fence.
			work &= roomMediaCommandAdmit | roomMediaCommandLeave | roomMediaCommandTrack
		}
		if work != 0 && actor.handler != nil {
			actor.handler(actor.roomID, work)
		}
		if terminal {
			actor.closed.Store(true)
			return
		}
		// A command may have landed while the handler was running after the
		// wake token was consumed. Re-arm without blocking the producer.
		if actor.pending.Load() != 0 {
			select {
			case actor.wake <- struct{}{}:
			default:
			}
		}
	}
}

func actorForRoom(roomID string) *roomMediaActor {
	return actorForRoomGeneration(roomID, 0)
}

func actorForRoomGeneration(roomID string, generation uint64) *roomMediaActor {
	roomID = normalizeRoomID(roomID)
	roomMediaActors.Lock()
	defer roomMediaActors.Unlock()
	if actor := roomMediaActors.actors[roomID]; actor != nil && !actor.closed.Load() && !actor.closing.Load() {
		if actor.generation == generation {
			return actor
		}
		// A changed media generation is a new sitting. Fence the prior owner
		// before installing the successor even if an old callback arrives late.
		actor.enqueue(roomMediaCommandClose)
	}
	actor := newRoomMediaActor(roomID, generation, func(roomID string, _ roomMediaCommand) {
		signalPeerConnectionsForRoomGenerationWithRestart(roomID, generation)
	})
	roomMediaActors.actors[roomID] = actor
	return actor
}

func roomMediaActorForGeneration(roomID string, generation uint64) *roomMediaActor {
	roomID = normalizeRoomID(roomID)
	roomMediaActors.Lock()
	actor := roomMediaActors.actors[roomID]
	roomMediaActors.Unlock()
	if actor == nil || actor.generation != generation || actor.closing.Load() || actor.closed.Load() {
		return nil
	}
	return actor
}

func requestRoomMediaCommand(roomID string, command roomMediaCommand) {
	roomID = normalizeRoomID(roomID)
	roomMediaActors.Lock()
	actor := roomMediaActors.actors[roomID]
	roomMediaActors.Unlock()
	if actor != nil {
		actor.enqueue(command)
	}
}

func requestRoomMediaCommandForGeneration(roomID string, generation uint64, command roomMediaCommand) bool {
	actor := roomMediaActorForGeneration(roomID, generation)
	if actor == nil {
		return false
	}
	return actor.enqueue(command)
}

// closeRoomMediaActor is called only after admission is fenced and the room is
// empty. It removes the actor from the registry before closing it, so a later
// sitting receives a fresh owner while callbacks holding the old actor cannot
// publish into the successor.
func closeRoomMediaActor(roomID string) {
	closeRoomMediaActorOwned(roomID, nil)
}

func closeRoomMediaActorOwned(roomID string, expected *roomMediaActor) {
	roomID = normalizeRoomID(roomID)
	roomMediaActors.Lock()
	actor := roomMediaActors.actors[roomID]
	if expected == nil || actor == expected {
		delete(roomMediaActors.actors, roomID)
	}
	roomMediaActors.Unlock()
	if expected != nil {
		actor = expected
	}
	if actor != nil {
		actor.enqueue(roomMediaCommandClose)
	}
}

func activeMediaRoomIDs() []string {
	rooms := map[string]struct{}{}
	listLock.RLock()
	for i := range peerConnections {
		rooms[normalizeRoomID(peerConnections[i].roomID)] = struct{}{}
	}
	for _, roomID := range trackRooms {
		rooms[normalizeRoomID(roomID)] = struct{}{}
	}
	listLock.RUnlock()
	ids := make([]string, 0, len(rooms))
	for roomID := range rooms {
		ids = append(ids, roomID)
	}
	sort.Strings(ids)
	return ids
}
