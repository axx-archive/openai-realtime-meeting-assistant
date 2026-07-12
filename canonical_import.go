package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const canonicalLegacyImportEventType = "legacy.object.imported"

var canonicalLegacyFamilies = []string{
	"memory", "artifact_revision", "board_card", "room", "guest_link", "meeting", "notification",
	"share_link", "file_folder", "file_assignment", "queue_job", "archive", "blob", "tombstone", "eviction",
}

// CanonicalImportPaths is explicit so tests and production shadow tooling can
// point at a fenced snapshot rather than accidentally reading the live data
// directory while it changes.
type CanonicalImportPaths struct {
	MeetingMemory  string
	Board          string
	Rooms          string
	Meetings       string
	Notifications  string
	ShareLinks     string
	FileFolders    string
	QueueDirs      []string
	ArchivesDir    string
	BlobsDir       string
	DeletedJournal string
	EvictedJournal string
}

type CanonicalImportedObject struct {
	Family           string
	ObjectID         string
	ObjectKey        string
	StateDigest      string
	AggregateVersion int64
	EventID          uuid.UUID
	RoomID           string
	MeetingID        string
	ContentRevision  int64
	ContentDigest    string
	ContentRef       string
	Status           string
	OccurredAt       time.Time
	Deleted          bool
	Principals       []string
}

type CanonicalImportPlan struct {
	TenantID string
	Objects  []CanonicalImportedObject
	Events   []CanonicalEvent
}

type CanonicalImporter struct {
	TenantID   string
	Paths      CanonicalImportPaths
	Versions   *FileCanonicalObjectVersionMap
	Registry   *CanonicalPayloadRegistry
	Principals func(CanonicalImportedObject) []string
}

func NewCanonicalImportPayloadRegistry() (*CanonicalPayloadRegistry, error) {
	registry := NewCanonicalPayloadRegistry()
	statuses := []string{"active", "archived", "open", "closed", "queued", "running", "complete", "completed", "failed", "approval_required", "cancelled", "revoked", "expired", "unknown"}
	if err := registry.Register(canonicalLegacyImportEventType, 1, CanonicalPayloadSchema{Fields: map[string]CanonicalPayloadField{
		"object_id":        {Kind: CanonicalPayloadIdentifier, Required: true},
		"source_kind":      {Kind: CanonicalPayloadEnum, Required: true, Enums: canonicalLegacyFamilies},
		"source_revision":  {Kind: CanonicalPayloadRevision, Required: true},
		"room_id":          {Kind: CanonicalPayloadIdentifier, Required: true},
		"meeting_id":       {Kind: CanonicalPayloadIdentifier},
		"status":           {Kind: CanonicalPayloadEnum, Required: true, Enums: statuses},
		"content_revision": {Kind: CanonicalPayloadRevision},
		"content_sha256":   {Kind: CanonicalPayloadDigest},
		"payload_sha256":   {Kind: CanonicalPayloadDigest, Required: true},
		"content_ref":      {Kind: CanonicalPayloadContentRef},
		"deleted":          {Kind: CanonicalPayloadBoolean, Required: true},
	}}); err != nil {
		return nil, err
	}
	return registry, nil
}

func (importer *CanonicalImporter) Build(ctx context.Context) (CanonicalImportPlan, error) {
	if importer == nil || strings.TrimSpace(importer.TenantID) == "" || importer.Versions == nil {
		return CanonicalImportPlan{}, errors.New("tenant and durable version map are required")
	}
	if importer.Registry == nil {
		registry, err := NewCanonicalImportPayloadRegistry()
		if err != nil {
			return CanonicalImportPlan{}, err
		}
		importer.Registry = registry
	}
	objects, err := importer.readLegacyObjects()
	if err != nil {
		return CanonicalImportPlan{}, err
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].Family != objects[j].Family {
			return objects[i].Family < objects[j].Family
		}
		return objects[i].ObjectID < objects[j].ObjectID
	})
	plan := CanonicalImportPlan{TenantID: importer.TenantID}
	for _, object := range objects {
		version, _, err := importer.Versions.ResolveVersionDurably(ctx, object.Family, object.ObjectKey, object.StateDigest)
		if err != nil {
			return CanonicalImportPlan{}, err
		}
		object.AggregateVersion = version
		object.EventID, err = CanonicalImportEventID(importer.TenantID, object.Family, object.ObjectKey, canonicalLegacyImportEventType, object.StateDigest)
		if err != nil {
			return CanonicalImportPlan{}, err
		}
		if importer.Principals != nil {
			object.Principals = uniqueSortedStrings(importer.Principals(object))
		}
		payloadValue := map[string]any{
			"object_id": object.ObjectID, "source_kind": object.Family, "source_revision": version,
			"room_id": NormalizeCanonicalRoomID(object.RoomID), "status": canonicalImportStatus(object.Status), "deleted": object.Deleted,
			"payload_sha256": object.StateDigest,
		}
		if object.MeetingID != "" {
			payloadValue["meeting_id"] = object.MeetingID
		}
		if object.ContentRevision > 0 && isHexDigest(object.ContentDigest) {
			payloadValue["content_revision"] = object.ContentRevision
			payloadValue["content_sha256"] = object.ContentDigest
		}
		if object.ContentRef != "" {
			payloadValue["content_ref"] = object.ContentRef
		}
		payload, payloadDigest, err := NewCanonicalEventPayload(importer.Registry, canonicalLegacyImportEventType, 1, payloadValue)
		if err != nil {
			return CanonicalImportPlan{}, fmt.Errorf("payload %s/%s: %w", object.Family, object.ObjectID, err)
		}
		occurredAt := object.OccurredAt.UTC()
		if occurredAt.IsZero() {
			occurredAt = time.Unix(0, 0).UTC()
		}
		event := CanonicalEvent{
			EventID: object.EventID, TenantID: importer.TenantID, AggregateType: object.Family, AggregateID: object.ObjectID,
			AggregateVersion: version, EventType: canonicalLegacyImportEventType, SchemaVersion: 1,
			OccurredAt: occurredAt, RecordedAt: occurredAt, Actor: CanonicalPrincipalRef{Kind: "service", ID: "legacy-import"},
			RoomID: NormalizeCanonicalRoomID(object.RoomID), MeetingID: object.MeetingID,
			IdempotencyKey: "legacy-import/" + object.EventID.String(), Classification: "internal", ACLVersion: 1,
			Payload: payload, ContentRef: object.ContentRef, PayloadSHA256: payloadDigest,
		}
		plan.Objects = append(plan.Objects, object)
		plan.Events = append(plan.Events, event)
	}
	return plan, nil
}

func (plan CanonicalImportPlan) Apply(ctx context.Context, store CanonicalEventStore) error {
	if store == nil {
		return errors.New("canonical event store is required")
	}
	for _, event := range plan.Events {
		if _, err := store.Append(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (importer *CanonicalImporter) readLegacyObjects() ([]CanonicalImportedObject, error) {
	var objects []CanonicalImportedObject
	readers := []func() ([]CanonicalImportedObject, error){
		func() ([]CanonicalImportedObject, error) { return importMemoryObjects(importer.Paths.MeetingMemory) },
		func() ([]CanonicalImportedObject, error) { return importBoardObjects(importer.Paths.Board) },
		func() ([]CanonicalImportedObject, error) { return importRoomObjects(importer.Paths.Rooms) },
		func() ([]CanonicalImportedObject, error) { return importMeetingObjects(importer.Paths.Meetings) },
		func() ([]CanonicalImportedObject, error) {
			return importNotificationObjects(importer.Paths.Notifications)
		},
		func() ([]CanonicalImportedObject, error) { return importShareLinkObjects(importer.Paths.ShareLinks) },
		func() ([]CanonicalImportedObject, error) { return importFileFolderObjects(importer.Paths.FileFolders) },
		func() ([]CanonicalImportedObject, error) { return importQueueObjects(importer.Paths.QueueDirs) },
		func() ([]CanonicalImportedObject, error) { return importArchiveObjects(importer.Paths.ArchivesDir) },
		func() ([]CanonicalImportedObject, error) { return importBlobObjects(importer.Paths.BlobsDir) },
		func() ([]CanonicalImportedObject, error) {
			return importLifecycleJournal(importer.Paths.DeletedJournal, "tombstone")
		},
		func() ([]CanonicalImportedObject, error) {
			return importLifecycleJournal(importer.Paths.EvictedJournal, "eviction")
		},
	}
	for _, read := range readers {
		familyObjects, err := read()
		if err != nil {
			return nil, err
		}
		objects = append(objects, familyObjects...)
	}
	seen := map[string]string{}
	for _, object := range objects {
		key := object.Family + "\x00" + object.ObjectID
		if prior, ok := seen[key]; ok && prior != object.StateDigest {
			return nil, fmt.Errorf("conflicting duplicate legacy object %s/%s", object.Family, object.ObjectID)
		}
		seen[key] = object.StateDigest
	}
	unique := objects[:0]
	added := map[string]struct{}{}
	for _, object := range objects {
		key := object.Family + "\x00" + object.ObjectID + "\x00" + object.StateDigest
		if _, ok := added[key]; ok {
			continue
		}
		added[key] = struct{}{}
		unique = append(unique, object)
	}
	return unique, nil
}

func importedObject(family, id string, safeState any, occurred time.Time) (CanonicalImportedObject, error) {
	key, err := CanonicalLegacyObjectKey(family, id)
	if err != nil {
		return CanonicalImportedObject{}, err
	}
	digest, err := CanonicalStateDigest(safeState)
	if err != nil {
		return CanonicalImportedObject{}, err
	}
	return CanonicalImportedObject{Family: family, ObjectID: id, ObjectKey: key, StateDigest: digest, OccurredAt: occurred, Status: "active"}, nil
}

func importMemoryObjects(path string) ([]CanonicalImportedObject, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var objects []CanonicalImportedObject
	// meetingMemoryStore owns one global seen[id] map across every kind. Its
	// boot load makes the first occurrence authoritative; all later duplicate
	// IDs are non-events even when their kind/body conflicts.
	seenIDs := map[string]struct{}{}
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var entry meetingMemoryEntry
			if err := json.Unmarshal(line, &entry); err != nil {
				return nil, fmt.Errorf("decode meeting memory: %w", err)
			}
			if strings.TrimSpace(entry.ID) != "" {
				if _, duplicate := seenIDs[entry.ID]; duplicate {
					if readErr != nil && readErr != io.EOF {
						return nil, readErr
					}
					if readErr == io.EOF {
						break
					}
					continue
				}
				seenIDs[entry.ID] = struct{}{}
				contentDigest := sha256.Sum256([]byte(entry.Text))
				metadataDigest, err := digestAny(entry.Metadata)
				if err != nil {
					return nil, err
				}
				safe := map[string]any{"id": entry.ID, "kind": entry.Kind, "room": NormalizeCanonicalRoomID(entry.Metadata["roomId"]), "meeting": entry.Metadata["meetingId"], "content_sha256": hex.EncodeToString(contentDigest[:]), "metadata_sha256": metadataDigest}
				object, err := importedObject("memory", entry.ID, safe, entry.CreatedAt)
				if err != nil {
					return nil, err
				}
				object.RoomID, object.MeetingID = NormalizeCanonicalRoomID(entry.Metadata["roomId"]), strings.TrimSpace(entry.Metadata["meetingId"])
				object.ContentRevision = int64(artifactVersion(entry))
				object.ContentDigest = hex.EncodeToString(contentDigest[:])
				object.ContentRef = "legacy:memory:" + entry.ID
				object.Status = firstNonEmptyString(entry.Metadata["status"], "active")
				objects = append(objects, object)
				if entry.Kind == meetingMemoryKindOSArtifact {
					for _, revision := range artifactVersionHistory(entry) {
						revisionID := artifactVersionID(entry.ID, revision.V)
						revisionSafe := map[string]any{"artifact_id": entry.ID, "version": revision.V, "body_ref": revision.BodyBlobRef, "at": revision.At}
						revisionObject, err := importedObject("artifact_revision", revisionID, revisionSafe, parseImportTime(revision.At))
						if err != nil {
							return nil, err
						}
						revisionObject.ContentRevision = int64(revision.V)
						if validBlobRef(revision.BodyBlobRef) {
							revisionObject.ContentDigest = revision.BodyBlobRef
							revisionObject.ContentRef = "blob:" + revision.BodyBlobRef
						}
						objects = append(objects, revisionObject)
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}
	return objects, nil
}

func importBoardObjects(path string) ([]CanonicalImportedObject, error) {
	var state kanbanBoardState
	if ok, err := readJSONIfExists(path, &state); err != nil || !ok {
		return nil, err
	}
	var objects []CanonicalImportedObject
	for _, card := range state.Cards {
		stateDigest, err := digestAny(card)
		if err != nil {
			return nil, err
		}
		object, err := importedObject("board_card", card.ID, map[string]any{"id": card.ID, "state_sha256": stateDigest}, parseImportTime(state.UpdatedAt))
		if err != nil {
			return nil, err
		}
		object.Status = string(card.Status)
		objects = append(objects, object)
	}
	return objects, nil
}

func importRoomObjects(path string) ([]CanonicalImportedObject, error) {
	var rooms []roomRecord
	if ok, err := readJSONIfExists(path, &rooms); err != nil || !ok {
		return nil, err
	}
	var objects []CanonicalImportedObject
	for _, room := range rooms {
		safe := map[string]any{"id": room.ID, "archived": room.Archived, "guest_enabled": room.GuestEnabled, "passcode_digest": digestText(room.PasscodeHash)}
		object, err := importedObject("room", room.ID, safe, room.CreatedAt)
		if err != nil {
			return nil, err
		}
		object.RoomID = NormalizeCanonicalRoomID(room.ID)
		if room.Archived {
			object.Status = "archived"
		}
		objects = append(objects, object)
		for _, link := range room.GuestLinks {
			linkObject, err := importedObject("guest_link", room.ID+":"+link.ID, map[string]any{"id": link.ID, "token_hash_digest": digestText(link.Hash), "expires": link.Expires, "revoked": link.Revoked}, link.CreatedAt)
			if err != nil {
				return nil, err
			}
			linkObject.RoomID = object.RoomID
			if link.Revoked {
				linkObject.Status = "revoked"
			}
			objects = append(objects, linkObject)
		}
	}
	return objects, nil
}

func importMeetingObjects(path string) ([]CanonicalImportedObject, error) {
	var state meetingStoreState
	if ok, err := readJSONIfExists(path, &state); err != nil || !ok {
		return nil, err
	}
	var objects []CanonicalImportedObject
	for _, record := range state.Meetings {
		participantsDigest, err := digestAny(record.Participants)
		if err != nil {
			return nil, err
		}
		object, err := importedObject("meeting", record.ID, map[string]any{"id": record.ID, "room": meetingRoomID(record), "started": record.StartedAt, "ended": record.EndedAt, "archive": record.ArchiveID, "participants_digest": participantsDigest}, parseImportTime(record.StartedAt))
		if err != nil {
			return nil, err
		}
		object.RoomID, object.MeetingID = meetingRoomID(record), record.ID
		if record.EndedAt == "" {
			object.Status = "open"
		} else {
			object.Status = "closed"
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func importNotificationObjects(path string) ([]CanonicalImportedObject, error) {
	var state notificationStoreState
	if ok, err := readJSONIfExists(path, &state); err != nil || !ok {
		return nil, err
	}
	var objects []CanonicalImportedObject
	for _, record := range state.Notifications {
		readDigest, err := digestAny(append([]string{}, record.ReadBy...))
		if err != nil {
			return nil, err
		}
		clearedDigest, err := digestAny(append([]string{}, record.ClearedBy...))
		if err != nil {
			return nil, err
		}
		safe := map[string]any{"id": record.ID, "kind": record.Kind, "body_digest": digestText(record.Text), "recipient_digest": digestText(record.UserEmail), "artifact": record.ArtifactID, "thread": record.ThreadID, "resolved": record.ResolvedAt, "read_digest": readDigest, "cleared_digest": clearedDigest}
		object, err := importedObject("notification", record.ID, safe, parseImportTime(record.CreatedAt))
		if err != nil {
			return nil, err
		}
		if record.ResolvedAt != "" {
			object.Status = "closed"
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func importShareLinkObjects(path string) ([]CanonicalImportedObject, error) {
	var records []shareLinkRecord
	if ok, err := readJSONIfExists(path, &records); err != nil || !ok {
		return nil, err
	}
	var objects []CanonicalImportedObject
	for _, record := range records {
		tokenHash := sha256.Sum256([]byte(record.Token))
		safe := map[string]any{"id": record.ID, "artifact": record.ArtifactID, "token_sha256": hex.EncodeToString(tokenHash[:]), "status": record.Status, "expires": record.ExpiresAt, "open_count": record.OpenCount}
		object, err := importedObject("share_link", record.ID, safe, parseImportTime(record.CreatedAt))
		if err != nil {
			return nil, err
		}
		object.Status = record.Status
		objects = append(objects, object)
	}
	return objects, nil
}

func importFileFolderObjects(path string) ([]CanonicalImportedObject, error) {
	var state fileFolderStoreState
	if ok, err := readJSONIfExists(path, &state); err != nil || !ok {
		return nil, err
	}
	var objects []CanonicalImportedObject
	for _, folder := range state.Folders {
		object, err := importedObject("file_folder", folder.ID, map[string]any{"id": folder.ID, "name_digest": digestText(folder.Name)}, parseImportTime(folder.CreatedAt))
		if err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	for fileID, folderID := range state.Assignments {
		id := fileID + ":" + folderID
		object, err := importedObject("file_assignment", id, map[string]any{"file": fileID, "folder": folderID}, time.Time{})
		if err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func importQueueObjects(dirs []string) ([]CanonicalImportedObject, error) {
	var objects []CanonicalImportedObject
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, err
			}
			var generic struct {
				ID         string    `json:"id"`
				ArtifactID string    `json:"artifact_id"`
				ThreadID   string    `json:"thread_id"`
				Status     string    `json:"status"`
				Authority  string    `json:"authority"`
				CreatedAt  time.Time `json:"created_at"`
				Attempts   int       `json:"attempts"`
			}
			if err := json.Unmarshal(raw, &generic); err != nil {
				return nil, err
			}
			if generic.ID == "" {
				continue
			}
			object, err := importedObject("queue_job", generic.ID, map[string]any{"id": generic.ID, "artifact": generic.ArtifactID, "thread": generic.ThreadID, "status": generic.Status, "authority": generic.Authority, "attempts": generic.Attempts, "raw_digest": digestBytes(raw)}, generic.CreatedAt)
			if err != nil {
				return nil, err
			}
			object.Status = generic.Status
			objects = append(objects, object)
		}
	}
	return objects, nil
}

func importArchiveObjects(dir string) ([]CanonicalImportedObject, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var objects []CanonicalImportedObject
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var archive meetingArchive
		if err := json.Unmarshal(raw, &archive); err != nil {
			return nil, err
		}
		if archive.ID == "" {
			continue
		}
		object, err := importedObject("archive", archive.ID, map[string]any{"id": archive.ID, "meeting": archive.MeetingID, "content_sha256": digestBytes(raw), "memory_count": len(archive.Memory), "card_count": len(archive.Board.Cards)}, archive.ArchivedAt)
		if err != nil {
			return nil, err
		}
		object.MeetingID, object.ContentRevision, object.ContentDigest, object.ContentRef, object.Status = archive.MeetingID, 1, digestBytes(raw), "legacy:archive:"+archive.ID, "archived"
		objects = append(objects, object)
	}
	return objects, nil
}

func importBlobObjects(dir string) ([]CanonicalImportedObject, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	var objects []CanonicalImportedObject
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), blobMetaSuffix) {
			return nil
		}
		ref := strings.TrimSuffix(entry.Name(), blobMetaSuffix)
		if !validBlobRef(ref) {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var meta blobMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			return err
		}
		object, err := importedObject("blob", ref, map[string]any{"ref": ref, "mime": meta.Mime, "size": meta.Size, "created": meta.CreatedAt}, parseImportTime(meta.CreatedAt))
		if err != nil {
			return err
		}
		object.ContentRevision, object.ContentDigest, object.ContentRef = 1, ref, "blob:"+ref
		objects = append(objects, object)
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return objects, err
}

type CanonicalLifecycleJournalRecord struct {
	Family      string    `json:"family"`
	ObjectID    string    `json:"object_id"`
	StateDigest string    `json:"state_sha256"`
	At          time.Time `json:"at"`
	Reason      string    `json:"reason"`
}

func importLifecycleJournal(path, family string) ([]CanonicalImportedObject, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var objects []CanonicalImportedObject
	for scanner.Scan() {
		var record CanonicalLifecycleJournalRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		id := record.Family + ":" + record.ObjectID
		object, err := importedObject(family, id, map[string]any{"family": record.Family, "object": record.ObjectID, "state": record.StateDigest, "reason_digest": digestText(record.Reason)}, record.At)
		if err != nil {
			return nil, err
		}
		object.Deleted, object.Status = true, "closed"
		objects = append(objects, object)
	}
	return objects, scanner.Err()
}

func readJSONIfExists(path string, target any) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return false, nil
	}
	return true, json.Unmarshal(raw, target)
}

func digestAny(value any) (string, error) {
	raw, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	return digestBytes(raw), nil
}
func digestText(value string) string  { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func parseImportTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	return parsed
}

func canonicalImportStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "active", "archived", "open", "closed", "queued", "running", "complete", "completed", "failed", "approval_required", "cancelled", "revoked", "expired":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "unknown"
	}
}
