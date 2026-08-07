package main

import (
	"os"
	"testing"
)

func TestRenderRunnerJobFilesRemainGroupWritable(t *testing.T) {
	store := newRenderRunnerJobStore(t.TempDir())
	job, err := store.enqueue(renderRunnerJob{
		ID: "render-job-mode", ArtifactID: "artifact-mode", Kind: renderJobKindDeck,
		HTML: "<!doctype html><html><body>mode</body></html>",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertMode := func(stage string) {
		t.Helper()
		info, err := os.Stat(store.jobPath(job.ID))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o660 {
			t.Fatalf("%s render job mode=%#o, want 0660", stage, got)
		}
	}
	assertMode("enqueued")
	job.Status = renderJobStatusRunning
	if err := store.update(job); err != nil {
		t.Fatal(err)
	}
	assertMode("updated")
}
