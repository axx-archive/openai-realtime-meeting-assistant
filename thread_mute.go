package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Per-thread mute — design §8 of docs/plans/the-table-design.md.
//
// This ships WITH the push default that creates the need, not in a later wave.
// Shipping "every Table message buzzes" without the valve is the version where
// someone turns notifications off at the OS level — and that is unrecoverable,
// because you never get a second chance to ask.

type threadMuteRecord struct {
	TenantID  string `json:"tenantId,omitempty"`
	UserEmail string `json:"userEmail"`
	ThreadID  string `json:"threadId"`
	MutedAt   string `json:"mutedAt"`
	// Level is "mentions" or "none". An absent value is a legacy mute and
	// therefore means mentions; "all" is represented by no row at all.
	Level string `json:"level,omitempty"`
}

type threadMuteStoreData struct {
	Mutes     []threadMuteRecord `json:"mutes"`
	UpdatedAt string             `json:"updatedAt,omitempty"`
}

var threadMuteStoreMu sync.Mutex

func threadMutesPath() string {
	if path := strings.TrimSpace(os.Getenv("THREAD_MUTES_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "thread-mutes.json")
}

func loadThreadMuteStoreFile() threadMuteStoreData {
	raw, err := os.ReadFile(threadMutesPath())
	if err != nil {
		return threadMuteStoreData{}
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return threadMuteStoreData{}
	}
	var state threadMuteStoreData
	if err := json.Unmarshal(raw, &state); err != nil {
		log.Errorf("Failed to decode thread mutes: %v", err)
		return threadMuteStoreData{}
	}
	for index := range state.Mutes {
		state.Mutes[index].UserEmail = normalizeAccountEmail(state.Mutes[index].UserEmail)
		state.Mutes[index].ThreadID = strings.TrimSpace(state.Mutes[index].ThreadID)
		state.Mutes[index].Level = normalizeThreadNotificationLevel(firstNonEmptyString(state.Mutes[index].Level, threadNotificationMentions))
	}
	return state
}

const (
	threadNotificationAll      = "all"
	threadNotificationMentions = "mentions"
	threadNotificationNone     = "none"
)

func normalizeThreadNotificationLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case threadNotificationMentions:
		return threadNotificationMentions
	case threadNotificationNone:
		return threadNotificationNone
	default:
		return threadNotificationAll
	}
}

func setThreadNotificationLevel(tenantID, userEmail, threadID, level string) error {
	userEmail = normalizeAccountEmail(userEmail)
	threadID = strings.TrimSpace(threadID)
	level = normalizeThreadNotificationLevel(level)
	if userEmail == "" || threadID == "" {
		return nil
	}
	return mutateThreadMuteStore(func(state *threadMuteStoreData) {
		kept := state.Mutes[:0]
		for _, existing := range state.Mutes {
			if existing.UserEmail == userEmail && existing.ThreadID == threadID {
				continue
			}
			kept = append(kept, existing)
		}
		state.Mutes = kept
		if level != threadNotificationAll {
			state.Mutes = append(state.Mutes, threadMuteRecord{
				TenantID: strings.TrimSpace(tenantID), UserEmail: userEmail, ThreadID: threadID,
				MutedAt: time.Now().UTC().Format(time.RFC3339), Level: level,
			})
		}
	})
}

func threadNotificationLevel(tenantID, userEmail, threadID string) string {
	userEmail = normalizeAccountEmail(userEmail)
	threadID = strings.TrimSpace(threadID)
	if userEmail == "" || threadID == "" {
		return threadNotificationAll
	}
	for _, record := range snapshotThreadMuteStore().Mutes {
		if record.UserEmail == userEmail && record.ThreadID == threadID {
			return normalizeThreadNotificationLevel(firstNonEmptyString(record.Level, threadNotificationMentions))
		}
	}
	return threadNotificationAll
}

func mutateThreadMuteStore(fn func(*threadMuteStoreData)) error {
	threadMuteStoreMu.Lock()
	defer threadMuteStoreMu.Unlock()
	state := loadThreadMuteStoreFile()
	fn(&state)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeJSONFileAtomically(threadMutesPath(), "thread mutes", state)
}

func snapshotThreadMuteStore() threadMuteStoreData {
	threadMuteStoreMu.Lock()
	defer threadMuteStoreMu.Unlock()
	return loadThreadMuteStoreFile()
}

func setThreadMuted(tenantID, userEmail, threadID string, muted bool) error {
	level := threadNotificationAll
	if muted {
		level = threadNotificationMentions
	}
	return setThreadNotificationLevel(tenantID, userEmail, threadID, level)
}

func threadMuted(tenantID, userEmail, threadID string) bool {
	userEmail = normalizeAccountEmail(userEmail)
	threadID = strings.TrimSpace(threadID)
	if userEmail == "" || threadID == "" {
		return false
	}
	return threadNotificationLevel(tenantID, userEmail, threadID) != threadNotificationAll
}

// threadMutedForUser decides whether a mute should suppress THIS record.
//
// Mute silences ambient volume only. A direct mention still delivers: muting a
// channel means "stop buzzing me for every message", never "make me
// unreachable", and conflating those is how someone misses being paged.
//
// The distinction comes free from the server's own shape — a targeted
// notification carries UserEmail, a broadcast channel post does not — so it is
// never re-derived from message text.
func threadMutedForUser(userEmail string, record notificationRecord) bool {
	threadID := strings.TrimSpace(record.ThreadID)
	if threadID == "" {
		return false
	}
	switch threadNotificationLevel("", userEmail, threadID) {
	case threadNotificationNone:
		return true
	case threadNotificationMentions:
		return strings.TrimSpace(record.UserEmail) == ""
	default:
		return false
	}
}

// deviceLaneDelivers is the pure predicate behind the lane's mute decision,
// extracted so the rule is testable without a transport.
func deviceLaneDelivers(record notificationRecord, muted bool) bool {
	if !muted {
		return true
	}
	// A direct mention carries a recipient and always delivers, mute or not.
	return strings.TrimSpace(record.UserEmail) != ""
}

func assistantThreadMuteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	if kanbanApp == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "chat threads are unavailable")
		return
	}

	payload := struct {
		ThreadID string `json:"threadId"`
		Muted    *bool  `json:"muted"`
		Level    string `json:"level"`
	}{}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload)
	}
	threadID := strings.TrimSpace(payload.ThreadID)
	if threadID == "" {
		writeAuthError(w, http.StatusBadRequest, "threadId is required")
		return
	}
	if _, _, err := kanbanApp.scoutChatThreadByID(user.Email, threadID); err != nil {
		writeAuthError(w, http.StatusNotFound, "chat thread not found")
		return
	}

	level := strings.ToLower(strings.TrimSpace(payload.Level))
	if level == "" && payload.Muted != nil {
		level = threadNotificationAll
		if *payload.Muted {
			level = threadNotificationMentions
		}
	}
	if level != threadNotificationAll && level != threadNotificationMentions && level != threadNotificationNone {
		writeAuthError(w, http.StatusBadRequest, "level must be all, mentions, or none")
		return
	}
	if err := setThreadNotificationLevel("", user.Email, threadID, level); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not save mute state")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"ok": true, "muted": level != threadNotificationAll, "level": level})
}
