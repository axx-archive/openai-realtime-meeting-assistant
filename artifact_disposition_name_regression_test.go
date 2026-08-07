package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type namedDispositionEffects struct {
	mu    sync.Mutex
	saves int
}

func (*namedDispositionEffects) Open(context.Context, ArtifactDispositionRef) error { return nil }

func (effects *namedDispositionEffects) Save(_ context.Context, ref ArtifactDispositionRef, actor, folderID, fileName string) (ArtifactDriveReference, error) {
	effects.mu.Lock()
	effects.saves++
	effects.mu.Unlock()
	return ArtifactDriveReference{
		ID: ref.ArtifactID, Name: fileName, Artifact: ref, CreatedAt: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
		CreatedBy: actor, FolderID: folderID, SourceArtifactID: ref.ArtifactID,
	}, nil
}

func (*namedDispositionEffects) Discard(context.Context, ArtifactDispositionRef, bool) (int, error) {
	return 0, nil
}

func (effects *namedDispositionEffects) saveCount() int {
	effects.mu.Lock()
	defer effects.mu.Unlock()
	return effects.saves
}

func TestNamedArtifactDispositionIsExactlyIdempotent(t *testing.T) {
	store, err := OpenArtifactDispositionStore(filepath.Join(t.TempDir(), "named-dispositions.json"), true, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ref := artifactDispositionTestRef("artifact-named-save", 1, "organization")
	request := ArtifactDispositionRequest{
		OperationID: "save-named-artifact", Action: ArtifactDispositionSave, ActorPrincipal: artifactDispositionTestActor,
		Artifact: ref, FolderID: "folder-board", FileName: "Board Brief.md",
	}
	effects := &namedDispositionEffects{}
	first, err := store.Apply(context.Background(), request, ref, effects)
	if err != nil || first.Outcome != "saved" || first.Drive == nil || first.Drive.Name != request.FileName || first.Drive.FolderID != request.FolderID {
		t.Fatalf("first named save=%+v err=%v", first, err)
	}
	second, err := store.Apply(context.Background(), request, ref, effects)
	if err != nil || second.Header.ContentDigest != first.Header.ContentDigest || second.Drive == nil || second.Drive.Name != request.FileName {
		t.Fatalf("idempotent named save=%+v err=%v", second, err)
	}
	if got := effects.saveCount(); got != 1 {
		t.Fatalf("save effects=%d, want exactly one", got)
	}

	changedName := request
	changedName.FileName = "Different Brief.md"
	if _, err := store.Apply(context.Background(), changedName, ref, effects); !errors.Is(err, ErrArtifactDispositionConflict) {
		t.Fatalf("operation reuse with changed name err=%v, want conflict", err)
	}
	if got := effects.saveCount(); got != 1 {
		t.Fatalf("conflicting replay executed save effect %d times", got)
	}
}
