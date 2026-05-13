package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	meetingMemoryKindTranscript = "transcript"
	meetingMemoryKindArchive    = "archive"
	defaultMeetingMemoryPath    = "data/meeting-memory.jsonl"
)

var memoryTokenPattern = regexp.MustCompile(`[a-z0-9]+`)

type meetingMemoryStore struct {
	mu      sync.Mutex
	path    string
	entries []meetingMemoryEntry
	seen    map[string]struct{}
}

type meetingMemoryEntry struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	Text      string            `json:"text"`
	CreatedAt time.Time         `json:"createdAt"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type meetingMemoryMatch struct {
	Entry meetingMemoryEntry
	Score int
}

func newMeetingMemoryStore(path string) (*meetingMemoryStore, error) {
	store := &meetingMemoryStore{
		path: path,
		seen: map[string]struct{}{},
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create memory directory: %w", err)
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry meetingMemoryEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			log.Warnf("Skipping malformed memory entry: %v", err)
			continue
		}
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.Text) == "" {
			continue
		}
		store.entries = append(store.entries, entry)
		store.seen[entry.ID] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read memory file: %w", err)
	}

	return store, nil
}

func meetingMemoryPath() string {
	if path := strings.TrimSpace(os.Getenv("MEETING_MEMORY_PATH")); path != "" {
		return path
	}

	return defaultMeetingMemoryPath
}

func (store *meetingMemoryStore) appendTranscript(eventID string, itemID string, transcript string) (meetingMemoryEntry, bool, error) {
	transcript = normalizeMemoryText(transcript)
	if store == nil || transcript == "" {
		return meetingMemoryEntry{}, false, nil
	}

	id := strings.TrimSpace(eventID)
	if id == "" {
		id = strings.TrimSpace(itemID)
	}
	if id == "" {
		id = fmt.Sprintf("transcript-%d", time.Now().UnixNano())
	}

	return store.appendEntry(meetingMemoryKindTranscript, id, transcript, map[string]string{
		"itemId": itemID,
	})
}

func (store *meetingMemoryStore) appendArchive(id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	return store.appendEntry(meetingMemoryKindArchive, id, text, metadata)
}

func (store *meetingMemoryStore) appendEntry(kind string, id string, text string, metadata map[string]string) (meetingMemoryEntry, bool, error) {
	text = normalizeMemoryText(text)
	if store == nil || text == "" {
		return meetingMemoryEntry{}, false, nil
	}
	if strings.TrimSpace(kind) == "" {
		kind = meetingMemoryKindTranscript
	}
	if strings.TrimSpace(id) == "" {
		id = fmt.Sprintf("%s-%d", kind, time.Now().UnixNano())
	}

	entry := meetingMemoryEntry{
		ID:        strings.TrimSpace(id),
		Kind:      strings.TrimSpace(kind),
		Text:      text,
		CreatedAt: time.Now().UTC(),
		Metadata:  metadata,
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if _, ok := store.seen[entry.ID]; ok {
		return entry, false, nil
	}

	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("open memory file: %w", err)
	}
	defer file.Close()

	raw, err := json.Marshal(entry)
	if err != nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("encode memory entry: %w", err)
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		return meetingMemoryEntry{}, false, fmt.Errorf("write memory entry: %w", err)
	}

	store.entries = append(store.entries, entry)
	store.seen[entry.ID] = struct{}{}

	return entry, true, nil
}

func (store *meetingMemoryStore) snapshot(limit int) []meetingMemoryEntry {
	if store == nil {
		return nil
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	return cloneMemoryEntries(tailMemoryEntries(store.entries, limit))
}

func (store *meetingMemoryStore) search(query string, limit int) []meetingMemoryMatch {
	if store == nil || limit <= 0 {
		return nil
	}

	query = normalizeMemoryText(query)
	if query == "" {
		return nil
	}

	queryTokens := uniqueMemoryTokens(query)
	if len(queryTokens) == 0 {
		return nil
	}

	store.mu.Lock()
	entries := cloneMemoryEntries(store.entries)
	store.mu.Unlock()

	matches := make([]meetingMemoryMatch, 0, len(entries))
	lowerQuery := strings.ToLower(query)
	for _, entry := range entries {
		lowerText := strings.ToLower(entry.Text)
		score := 0
		if strings.Contains(lowerText, lowerQuery) {
			score += 10
		}
		for _, token := range queryTokens {
			if strings.Contains(lowerText, token) {
				score += 3
			}
		}
		if score == 0 {
			continue
		}
		matches = append(matches, meetingMemoryMatch{Entry: entry, Score: score})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].Entry.CreatedAt.After(matches[j].Entry.CreatedAt)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}

	return matches
}

func buildMemoryAnswer(query string, matches []meetingMemoryMatch) string {
	query = normalizeMemoryText(query)
	if len(matches) == 0 {
		if query == "" {
			return "I do not have enough meeting memory yet."
		}
		return fmt.Sprintf("I could not find anything in meeting memory for %q.", query)
	}

	parts := make([]string, 0, len(matches)+1)
	parts = append(parts, fmt.Sprintf("I found %d relevant memory item(s) for %q:", len(matches), query))
	for _, match := range matches {
		parts = append(parts, fmt.Sprintf("- %s", match.Entry.Text))
	}

	return strings.Join(parts, "\n")
}

func normalizeMemoryText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func uniqueMemoryTokens(value string) []string {
	rawTokens := memoryTokenPattern.FindAllString(strings.ToLower(value), -1)
	seen := map[string]struct{}{}
	tokens := make([]string, 0, len(rawTokens))
	for _, token := range rawTokens {
		if len(token) < 3 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	return tokens
}

func tailMemoryEntries(entries []meetingMemoryEntry, limit int) []meetingMemoryEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}

	return entries[len(entries)-limit:]
}

func cloneMemoryEntries(entries []meetingMemoryEntry) []meetingMemoryEntry {
	cloned := make([]meetingMemoryEntry, 0, len(entries))
	for _, entry := range entries {
		cloned = append(cloned, cloneMemoryEntry(entry))
	}

	return cloned
}

func cloneMemoryEntry(entry meetingMemoryEntry) meetingMemoryEntry {
	cloned := entry
	if entry.Metadata != nil {
		cloned.Metadata = make(map[string]string, len(entry.Metadata))
		for key, value := range entry.Metadata {
			cloned.Metadata[key] = value
		}
	}

	return cloned
}
