package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultMeetingRoomPassword = "B0NFIRE!"
	defaultMeetingRoomCapacity = 7
)

var meetingParticipantNames = []string{
	"Joel",
	"Caitlyn",
	"Tyler",
	"AJ",
	"Tim",
	"Erick",
	"Tom",
}

func canonicalParticipantName(name string) string {
	normalizedName := strings.TrimSpace(name)
	for _, candidate := range meetingParticipantNames {
		if strings.EqualFold(normalizedName, candidate) {
			return candidate
		}
	}

	return ""
}

// guestNamePrefix is the server-enforced display prefix for guest seats
// (multi-room §5.2). It is stamped at admission and carried into presence,
// track bookkeeping, and transcript attribution — never applied only at the
// display layer — so a guest can never be recorded under a member's name.
const guestNamePrefix = "Guest "

// isGuestDisplayName recognizes a server-minted guest display name. Roster
// collision is rejected at /guest/join, so the prefix is unambiguous.
func isGuestDisplayName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), strings.ToLower(guestNamePrefix))
}

// canonicalRoomParticipantName resolves a name for room presence/media/
// attribution bookkeeping: seeded roster names canonicalize exactly as
// canonicalParticipantName; server-minted guest display names pass through
// verbatim (they only exist because admission stamped them). Anything else is
// rejected, preserving the roster-only contract for member paths.
func canonicalRoomParticipantName(name string) string {
	if canonical := canonicalParticipantName(name); canonical != "" {
		return canonical
	}
	trimmed := strings.TrimSpace(name)
	if isGuestDisplayName(trimmed) {
		return trimmed
	}
	return ""
}

const archiveSecretFileName = "archive-secret"

var (
	archiveSecretMu    sync.Mutex
	archiveSecretCache = map[string][]byte{}
)

type archiveCapabilityRecord struct {
	ArchiveID, TenantID, ContentDigest, Action, ExpiresAt, TokenHash string
	Revoked                                                          bool
}

var archiveCapabilityMu sync.Mutex

func archiveCapabilitiesPath() string {
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "archive-capabilities.json")
}

func loadArchiveCapabilities() (map[string]archiveCapabilityRecord, error) {
	raw, err := os.ReadFile(archiveCapabilitiesPath())
	if os.IsNotExist(err) {
		return map[string]archiveCapabilityRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records map[string]archiveCapabilityRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, err
	}
	if records == nil {
		records = map[string]archiveCapabilityRecord{}
	}
	return records, nil
}

func saveArchiveCapabilities(records map[string]archiveCapabilityRecord) error {
	return writeJSONFileAtomically(archiveCapabilitiesPath(), "archive capabilities", records)
}

// archiveTokenSecret returns the random 32-byte server secret that keys
// archive access tokens, created lazily next to the meeting memory file and
// loaded thereafter. Tokens are deliberately not derived from the room
// password: a leaked archive URL must not become an offline brute-force
// oracle for the room credential.
func archiveTokenSecret() []byte {
	path := filepath.Join(filepath.Dir(meetingMemoryPath()), archiveSecretFileName)

	archiveSecretMu.Lock()
	defer archiveSecretMu.Unlock()
	if secret, ok := archiveSecretCache[path]; ok {
		return secret
	}

	if raw, err := os.ReadFile(path); err == nil {
		if secret, decodeErr := hex.DecodeString(strings.TrimSpace(string(raw))); decodeErr == nil && len(secret) == 32 {
			archiveSecretCache[path] = secret
			return secret
		}
		log.Errorf("Ignoring malformed archive secret at %s; generating a new one", path)
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		log.Errorf("Failed to generate archive secret: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Errorf("Failed to create archive secret directory: %v; issued tokens will rotate on restart", err)
	} else if err := os.WriteFile(path, []byte(hex.EncodeToString(secret)+"\n"), 0o600); err != nil {
		log.Errorf("Failed to persist archive secret: %v; issued tokens will rotate on restart", err)
	}
	archiveSecretCache[path] = secret

	return secret
}

// archiveAccessToken derives a per-archive access key so server-issued
// archive links never carry the room password; a leaked URL grants access
// to that one archive only.
func archiveAccessToken(archiveID string) string {
	archiveID = strings.TrimSpace(strings.TrimSuffix(archiveID, ".json"))
	path, err := meetingArchivePath(archiveID)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	digestBytes := sha256.Sum256(raw)
	digest := hex.EncodeToString(digestBytes[:])
	archiveCapabilityMu.Lock()
	defer archiveCapabilityMu.Unlock()
	records, err := loadArchiveCapabilities()
	if err != nil {
		return ""
	}
	now := time.Now().UTC()
	record := records[archiveID]
	if record.ArchiveID == archiveID && record.Revoked {
		return ""
	}
	if record.ArchiveID == archiveID && record.TenantID == canonicalArtifactTenantID() && record.ContentDigest == digest && record.Action == "read_archive" && !record.Revoked {
		if expires, parseErr := time.Parse(time.RFC3339Nano, record.ExpiresAt); parseErr == nil && now.Before(expires) {
			return archiveCapabilityRaw(record)
		}
	}
	record = archiveCapabilityRecord{ArchiveID: archiveID, TenantID: canonicalArtifactTenantID(), ContentDigest: digest, Action: "read_archive", ExpiresAt: now.AddDate(0, 0, 7).Format(time.RFC3339Nano)}
	token := archiveCapabilityRaw(record)
	tokenHash := sha256.Sum256([]byte(token))
	record.TokenHash = hex.EncodeToString(tokenHash[:])
	records[archiveID] = record
	if err := saveArchiveCapabilities(records); err != nil {
		return ""
	}
	return token
}

func archiveCapabilityRaw(record archiveCapabilityRecord) string {
	mac := hmac.New(sha256.New, archiveTokenSecret())
	mac.Write([]byte(strings.Join([]string{"bonfire-archive", record.TenantID, record.ArchiveID, record.ContentDigest, record.Action, record.ExpiresAt}, ":")))
	expires, _ := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	return strings.Join([]string{strconv.FormatInt(expires.Unix(), 10), record.ContentDigest, record.Action, hex.EncodeToString(mac.Sum(nil))}, ".")
}

// validArchiveKey accepts only the archive's derived token, compared in
// constant time. The room password is deliberately rejected: accepting it
// here made /archives/ an unauthenticated password-guessing oracle.
func validArchiveKey(archiveID, key string) bool {
	_, err := authorizeArchiveRead(archiveID, key)
	return err == nil
}

func authorizeArchiveRead(archiveID, key string) ([]byte, error) {
	archiveID = strings.TrimSpace(strings.TrimSuffix(archiveID, ".json"))
	archiveCapabilityMu.Lock()
	defer archiveCapabilityMu.Unlock()
	records, err := loadArchiveCapabilities()
	if err != nil {
		return nil, err
	}
	record, ok := records[archiveID]
	if !ok || record.Revoked || record.TenantID != canonicalArtifactTenantID() || record.Action != "read_archive" || !isHexDigest(record.ContentDigest) || !isHexDigest(record.TokenHash) {
		return nil, fmt.Errorf("unauthorized")
	}
	expires, err := time.Parse(time.RFC3339Nano, record.ExpiresAt)
	if err != nil || !time.Now().UTC().Before(expires) {
		return nil, fmt.Errorf("unauthorized")
	}
	provided := sha256.Sum256([]byte(strings.TrimSpace(key)))
	expected, err := hex.DecodeString(record.TokenHash)
	if err != nil || subtle.ConstantTimeCompare(provided[:], expected) != 1 {
		return nil, fmt.Errorf("unauthorized")
	}
	// Authenticate before touching archive bytes; only an authorized request
	// pays the body read needed to reject a replaced file.
	path, err := meetingArchivePath(archiveID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != record.ContentDigest {
		return nil, fmt.Errorf("archive content changed")
	}
	return raw, nil
}

func revokeArchiveCapability(archiveID string) error {
	archiveCapabilityMu.Lock()
	defer archiveCapabilityMu.Unlock()
	records, err := loadArchiveCapabilities()
	if err != nil {
		return err
	}
	id := strings.TrimSpace(strings.TrimSuffix(archiveID, ".json"))
	record, ok := records[id]
	if !ok {
		return fmt.Errorf("archive capability not found")
	}
	record.Revoked, record.TokenHash = true, ""
	records[id] = record
	return saveArchiveCapabilities(records)
}

func reissueArchiveCapability(archiveID string) (string, error) {
	archiveCapabilityMu.Lock()
	records, err := loadArchiveCapabilities()
	if err != nil {
		archiveCapabilityMu.Unlock()
		return "", err
	}
	id := strings.TrimSpace(strings.TrimSuffix(archiveID, ".json"))
	record, ok := records[id]
	if !ok || !record.Revoked {
		archiveCapabilityMu.Unlock()
		return "", fmt.Errorf("revoked archive capability not found")
	}
	delete(records, id)
	if err := saveArchiveCapabilities(records); err != nil {
		archiveCapabilityMu.Unlock()
		return "", err
	}
	archiveCapabilityMu.Unlock()
	token := archiveAccessToken(id)
	if token == "" {
		return "", fmt.Errorf("could not reissue archive capability")
	}
	return token, nil
}

func configuredMeetingRoomPassword() string {
	if password := strings.TrimSpace(os.Getenv("MEETING_ROOM_PASSWORD")); password != "" {
		return password
	}

	return defaultMeetingRoomPassword
}

func configuredMeetingRoomCapacity() int {
	rawCapacity := strings.TrimSpace(os.Getenv("MEETING_ROOM_MAX_PARTICIPANTS"))
	if rawCapacity == "" {
		return defaultMeetingRoomCapacity
	}

	capacity, err := strconv.Atoi(rawCapacity)
	if err != nil || capacity < 1 {
		return defaultMeetingRoomCapacity
	}

	return capacity
}

func participantEmail(name string) string {
	name = canonicalParticipantName(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(name), "guest ") {
		return ""
	}
	if strings.EqualFold(name, "Erick") {
		return "e@shareability.com"
	}

	return strings.ToLower(name) + "@shareability.com"
}

// guestNameCollidesWithRoster rejects guest display names that would
// impersonate a seeded member (multi-room §5.2). The comparison is EqualFold
// against the names the seeded roster resolves to via participantNameForEmail
// — deliberately NOT a canonicalizer-non-empty predicate, so legitimate
// non-roster names always pass.
func guestNameCollidesWithRoster(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	for _, seed := range seededAccounts {
		rosterName := participantNameForEmail(seed.Email)
		if rosterName != "" && strings.EqualFold(trimmed, rosterName) {
			return true
		}
	}
	return false
}

func participantNameForEmail(email string) string {
	normalizedEmail := normalizeAccountEmail(email)
	if normalizedEmail == "" {
		return ""
	}
	for _, seed := range seededAccounts {
		if normalizeAccountEmail(seed.Email) == normalizedEmail {
			return canonicalParticipantName(seed.Name)
		}
	}
	return ""
}

func participantNameForAccount(user *userAccount) string {
	if user == nil {
		return ""
	}
	if name := participantNameForEmail(user.Email); name != "" {
		return name
	}
	return canonicalParticipantName(user.Name)
}

func participantEmails(names []string) []string {
	emails := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		email := participantEmail(name)
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		emails = append(emails, email)
	}

	return emails
}
