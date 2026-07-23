package main

import (
	"testing"
	"time"
)

func resetRoomMediaActorsForTest(t *testing.T) {
	t.Helper()
	closeAll := func() {
		roomMediaActors.Lock()
		actors := make([]*roomMediaActor, 0, len(roomMediaActors.actors))
		for _, actor := range roomMediaActors.actors {
			actors = append(actors, actor)
		}
		roomMediaActors.actors = map[string]*roomMediaActor{}
		roomMediaActors.Unlock()
		for _, actor := range actors {
			actor.enqueue(roomMediaCommandClose)
		}
		for _, actor := range actors {
			select {
			case <-actor.done:
			case <-time.After(2 * time.Second):
				t.Errorf("room media actor %s did not stop", actor.roomID)
			}
		}
	}
	closeAll()
	t.Cleanup(closeAll)
}
