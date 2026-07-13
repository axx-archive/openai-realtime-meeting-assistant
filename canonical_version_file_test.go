package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileCanonicalObjectVersionMapResolvesImportBatchDurably(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.json")
	versionMap, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	requests := []canonicalVersionRequest{
		{Family: "memory", ObjectKey: "one", StateDigest: captureDigest("a")},
		{Family: "memory", ObjectKey: "two", StateDigest: captureDigest("b")},
		{Family: "memory", ObjectKey: "one", StateDigest: captureDigest("c")},
	}
	results, err := versionMap.ResolveVersionsDurably(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results[0].Version != 1 || results[0].Existing || results[1].Version != 1 || results[1].Existing || results[2].Version != 2 || results[2].Existing {
		t.Fatalf("batch results=%+v", results)
	}
	firstBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := reloaded.ResolveVersionsDurably(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range retry {
		if !result.Existing || result.Version != results[index].Version {
			t.Fatalf("retry[%d]=%+v want existing version %d", index, result, results[index].Version)
		}
	}
	secondBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("all-existing batch rewrote durable version map")
	}
}

func TestFileCanonicalObjectVersionMapResolvesLargeImportBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.json")
	versionMap, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	const count = 5000
	requests := make([]canonicalVersionRequest, count)
	for index := range requests {
		key := fmt.Sprintf("legacy-object-%05d", index)
		requests[index] = canonicalVersionRequest{Family: "memory", ObjectKey: key, StateDigest: captureDigest(key)}
	}
	results, err := versionMap.ResolveVersionsDurably(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != count {
		t.Fatalf("results=%d want=%d", len(results), count)
	}
	for index, result := range results {
		if result.Version != 1 || result.Existing {
			t.Fatalf("result[%d]=%+v", index, result)
		}
	}
	reloaded, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reloaded.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != count {
		t.Fatalf("durable entries=%d want=%d", len(snapshot.Entries), count)
	}
}

func TestFileCanonicalObjectVersionMapPersistsAndLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.json")
	versionMap, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	digestA, digestB := captureDigest("a"), captureDigest("b")
	if version, _, err := versionMap.ResolveVersion("artifact", "a", digestA); err != nil || version != 1 {
		t.Fatalf("resolve = %d %v", version, err)
	}
	if version, _, err := versionMap.ResolveVersion("artifact", "a", digestB); err != nil || version != 2 {
		t.Fatalf("resolve = %d %v", version, err)
	}
	if version, _, err := versionMap.ResolveVersion("artifact", "unrelated", digestB); err != nil || version != 1 {
		t.Fatalf("resolve = %d %v", version, err)
	}
	if err := versionMap.PersistCurrent(context.Background()); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	if version, existing, err := reloaded.ResolveVersion("artifact", "a", digestA); err != nil || version != 1 || !existing {
		t.Fatalf("reload = %d %v %v", version, existing, err)
	}
	first, err := versionMap.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reloaded.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum != second.Checksum {
		t.Fatal("checksum changed after reload")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("version map mode = %o", info.Mode().Perm())
	}
}

func TestFileCanonicalObjectVersionMapStableUnderUnrelatedOrder(t *testing.T) {
	left, _ := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "left.json"))
	right, _ := OpenFileCanonicalObjectVersionMap(filepath.Join(t.TempDir(), "right.json"))
	entries := []struct{ object, digest string }{{"a", captureDigest("a")}, {"b", captureDigest("b")}, {"c", captureDigest("c")}}
	for _, entry := range entries {
		if _, _, err := left.ResolveVersion("board", entry.object, entry.digest); err != nil {
			t.Fatal(err)
		}
	}
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if _, _, err := right.ResolveVersion("board", entry.object, entry.digest); err != nil {
			t.Fatal(err)
		}
	}
	leftSnapshot, _ := left.Snapshot()
	rightSnapshot, _ := right.Snapshot()
	if leftSnapshot.Checksum != rightSnapshot.Checksum {
		t.Fatal("unrelated insertion order changed checksum")
	}
	for index := range leftSnapshot.Entries {
		if leftSnapshot.Entries[index] != rightSnapshot.Entries[index] {
			t.Fatal("unrelated insertion order changed versions")
		}
	}
}

func TestFileCanonicalObjectVersionMapRejectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.json")
	versionMap, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := versionMap.ResolveVersion("room", "r", captureDigest("r")); err != nil {
		t.Fatal(err)
	}
	if err := versionMap.PersistCurrent(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 1
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileCanonicalObjectVersionMap(path); err == nil {
		t.Fatal("corrupt version map loaded")
	}

	snapshot, err := versionMap.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Checksum[0] ^= 1
	if err := versionMap.Persist(context.Background(), snapshot); err == nil {
		t.Fatal("bad snapshot checksum persisted")
	}
}

func TestFileCanonicalObjectVersionMapDurableResolveRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	goodPath := filepath.Join(dir, "versions.json")
	versionMap, err := OpenFileCanonicalObjectVersionMap(goodPath)
	if err != nil {
		t.Fatal(err)
	}
	versionMap.path = filepath.Join(blocker, "versions.json")
	digest := captureDigest("state")
	if _, _, err := versionMap.ResolveVersionDurably(context.Background(), "artifact", "a", digest); err == nil {
		t.Fatal("expected durable allocation failure")
	}
	versionMap.path = goodPath
	version, existing, err := versionMap.ResolveVersionDurably(context.Background(), "artifact", "a", digest)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 || existing {
		t.Fatalf("failed allocation leaked into memory: version=%d existing=%v", version, existing)
	}
	reloaded, err := OpenFileCanonicalObjectVersionMap(versionMap.path)
	if err != nil {
		t.Fatal(err)
	}
	version, existing, err = reloaded.ResolveVersion("artifact", "a", digest)
	if err != nil || version != 1 || !existing {
		t.Fatalf("durable allocation did not reload: version=%d existing=%v err=%v", version, existing, err)
	}
}

func TestFileCanonicalObjectVersionMapCoordinatesIndependentInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.json")
	left, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	right, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	digests := []string{captureDigest("state-a"), captureDigest("state-b")}
	versions := make([]int64, 2)
	errorsByIndex := make([]error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		versions[0], _, errorsByIndex[0] = left.ResolveVersionDurably(context.Background(), "artifact", "same", digests[0])
	}()
	go func() {
		defer wait.Done()
		versions[1], _, errorsByIndex[1] = right.ResolveVersionDurably(context.Background(), "artifact", "same", digests[1])
	}()
	wait.Wait()
	if errorsByIndex[0] != nil || errorsByIndex[1] != nil {
		t.Fatalf("concurrent resolves = %v %v", errorsByIndex[0], errorsByIndex[1])
	}
	if versions[0] == versions[1] || (versions[0] != 1 && versions[1] != 1) || (versions[0] != 2 && versions[1] != 2) {
		t.Fatalf("versions lost or duplicated: %v", versions)
	}
	final, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	for index, digest := range digests {
		version, existing, err := final.ResolveVersionDurably(context.Background(), "artifact", "same", digest)
		if err != nil || !existing || version != versions[index] {
			t.Fatalf("digest %d reload = %d %v %v", index, version, existing, err)
		}
	}
	snapshot, err := final.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 2 {
		t.Fatalf("lost concurrent version entry: %+v", snapshot.Entries)
	}
}

func TestFileCanonicalObjectVersionMapRejectsStaleSnapshotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.json")
	stale, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	staleSnapshot, err := stale.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	writer, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := captureDigest("durable-new-state")
	if version, existing, err := writer.ResolveVersionDurably(context.Background(), "artifact", "a", digest); err != nil || existing || version != 1 {
		t.Fatalf("new durable allocation = %d %v %v", version, existing, err)
	}
	if err := stale.Persist(context.Background(), staleSnapshot); !errors.Is(err, ErrCanonicalStaleVersionSnapshot) {
		t.Fatalf("stale overwrite = %v", err)
	}
	final, err := OpenFileCanonicalObjectVersionMap(path)
	if err != nil {
		t.Fatal(err)
	}
	version, existing, err := final.ResolveVersionDurably(context.Background(), "artifact", "a", digest)
	if err != nil || !existing || version != 1 {
		t.Fatalf("new allocation was lost: version=%d existing=%v err=%v", version, existing, err)
	}
}
