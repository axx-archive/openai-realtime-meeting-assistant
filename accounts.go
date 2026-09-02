package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/bcrypt"
)

const (
	minAccountPasswordLength = 8
	resetTokenTTL            = 30 * time.Minute
)

type seededAccount struct {
	Email string
	Name  string
}

// seededAccounts is the complete roster of Bonfire accounts. There is no
// signup path: accounts exist only because they are listed here, and seeding
// never touches an account that already exists in the store file.
var seededAccounts = []seededAccount{
	{"joel@shareability.com", "Joel"},
	{"caitlyn@shareability.com", "Caitlyn"},
	{"tyler@shareability.com", "Tyler"},
	{"aj@shareability.com", "AJ"},
	{"tim@shareability.com", "Tim"},
	{"e@shareability.com", "Erick"},
	{"tom@shareability.com", "Tom"},
}

type userAccount struct {
	Email             string                `json:"email"`
	Name              string                `json:"name"`
	AvatarDataURL     string                `json:"avatarDataURL,omitempty"`
	PasswordHash      []byte                `json:"passwordHash"`
	WebAuthnHandle    []byte                `json:"webauthnHandle"`
	Credentials       []webauthn.Credential `json:"credentials,omitempty"`
	PasskeyAddedAt    map[string]time.Time  `json:"passkeyAddedAt,omitempty"`
	PasswordChangedAt time.Time             `json:"passwordChangedAt"`
	// ThemePref follows the user across devices: "light" | "dark" | "system";
	// empty means no account-level choice (client falls back to its device
	// storage, then the light product default — founder call 2026-07-10).
	ThemePref string `json:"themePref,omitempty"`
	// DisabledAt marks an offboarded account (Wave 5 D11). The record is
	// never deleted: history, authorship, and passkeys stay intact, but a
	// disabled account cannot sign in, its sessions are revoked on disable,
	// and directory surfaces (mentions, member pickers, human-group
	// validation, Drive grants) exclude it via accountIsDisabled. Nil means
	// active; re-enabling clears the stamp.
	DisabledAt *time.Time `json:"disabledAt,omitempty"`
}

// disabled reports whether the account is currently offboarded.
func (u *userAccount) disabled() bool {
	return u != nil && u.DisabledAt != nil && !u.DisabledAt.IsZero()
}

// accountIsDisabled is the one package-level filter every roster consumer
// (mention candidates, member pickers, human-group validation, Drive grants)
// applies. An unknown email is NOT disabled — callers that need existence
// check findUser separately, so this helper never widens a denial into an
// enumeration signal.
func accountIsDisabled(email string) bool {
	email = normalizeAccountEmail(email)
	if email == "" {
		return false
	}
	return accountStore().findUser(email).disabled()
}

// isFounderOwner is the owner notion the shell already projects as
// shellAccess=full for the founder account (shellAccessForSession): the one
// principal allowed to toggle account lifecycle. Deliberately NOT the
// organization owner/admin membership path, which can grant "full" to other
// members — offboarding stays with the founder.
func isFounderOwner(user *userAccount) bool {
	return user != nil && normalizeAccountEmail(user.Email) == founderOwnerEmail
}

// WebAuthnID implements webauthn.User with a stable random handle so passkeys
// keep working even if an email is ever re-cased or renamed.
func (u *userAccount) WebAuthnID() []byte                         { return u.WebAuthnHandle }
func (u *userAccount) WebAuthnName() string                       { return u.Email }
func (u *userAccount) WebAuthnDisplayName() string                { return u.Name }
func (u *userAccount) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

func (u *userAccount) credentialDescriptors() []protocol.CredentialDescriptor {
	descriptors := make([]protocol.CredentialDescriptor, 0, len(u.Credentials))
	for _, credential := range u.Credentials {
		descriptors = append(descriptors, credential.Descriptor())
	}
	return descriptors
}

type resetTokenRecord struct {
	email   string
	expires time.Time
}

type userAccountStore struct {
	mu          sync.Mutex
	path        string
	users       map[string]*userAccount
	resetTokens map[string]resetTokenRecord
}

func normalizeAccountEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func normalizeRosterLoginName(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

func seededAccountForName(name string) (seededAccount, bool) {
	normalized := normalizeRosterLoginName(name)
	for _, seed := range seededAccounts {
		if normalizeRosterLoginName(seed.Name) == normalized {
			return seed, true
		}
	}
	return seededAccount{}, false
}

func newUserAccountStore(path string) (*userAccountStore, error) {
	store := &userAccountStore{
		path:        path,
		users:       map[string]*userAccount{},
		resetTokens: map[string]resetTokenRecord{},
	}

	if raw, err := os.ReadFile(path); err == nil {
		var onDisk []*userAccount
		if err := json.Unmarshal(raw, &onDisk); err != nil {
			return nil, fmt.Errorf("malformed user store at %s: %w", path, err)
		}
		for _, user := range onDisk {
			if user == nil {
				return nil, fmt.Errorf("malformed user store at %s: nil account", path)
			}
			key := normalizeAccountEmail(user.Email)
			if key == "" {
				return nil, fmt.Errorf("malformed user store at %s: account has empty email", path)
			}
			if _, exists := store.users[key]; exists {
				return nil, fmt.Errorf("ambiguous user store at %s: duplicate normalized email %q", path, key)
			}
			store.users[key] = user
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := store.seedMissingAccounts(); err != nil {
		return nil, err
	}

	return store, nil
}

// seedMissingAccounts creates any roster account that is not already on disk
// with the configured starter password. Existing accounts are never modified,
// so changed passwords and registered passkeys survive restarts and redeploys.
func (s *userAccountStore) seedMissingAccounts() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for _, seed := range seededAccounts {
		key := normalizeAccountEmail(seed.Email)
		if _, exists := s.users[key]; exists {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(configuredMeetingRoomPassword()), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		handle := make([]byte, 32)
		if _, err := rand.Read(handle); err != nil {
			return err
		}
		s.users[key] = &userAccount{
			Email:             key,
			Name:              seed.Name,
			PasswordHash:      hash,
			WebAuthnHandle:    handle,
			PasswordChangedAt: time.Now().UTC(),
		}
		changed = true
	}

	if !changed {
		return nil
	}
	return s.persistLocked()
}

func (s *userAccountStore) persistLocked() error {
	accounts := make([]*userAccount, 0, len(s.users))
	seen := make(map[string]struct{}, len(seededAccounts))
	for _, seed := range seededAccounts {
		key := normalizeAccountEmail(seed.Email)
		if user, ok := s.users[key]; ok {
			accounts = append(accounts, user)
			seen[key] = struct{}{}
		}
	}
	extras := make([]string, 0, len(s.users)-len(seen))
	for key := range s.users {
		if _, ok := seen[key]; !ok {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		accounts = append(accounts, s.users[key])
	}

	raw, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	// Password hashes and passkeys need the same file-and-directory durability
	// guarantee as the company-brain stores even though they never become
	// canonical events.
	return writeFileAtomicallyDurable(s.path, raw, 0o600)
}

func (s *userAccountStore) accountEmails() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	emails := make([]string, 0, len(s.users))
	seen := make(map[string]struct{}, len(seededAccounts))
	for _, seed := range seededAccounts {
		key := normalizeAccountEmail(seed.Email)
		if user, ok := s.users[key]; ok {
			emails = append(emails, user.Email)
			seen[key] = struct{}{}
		}
	}
	extras := make([]string, 0, len(s.users)-len(seen))
	for key := range s.users {
		if _, ok := seen[key]; !ok {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		emails = append(emails, s.users[key].Email)
	}
	return emails
}

func (s *userAccountStore) findUser(email string) *userAccount {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[normalizeAccountEmail(email)]
}

func (s *userAccountStore) findUserByWebAuthnHandle(handle []byte) *userAccount {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if len(user.WebAuthnHandle) > 0 && subtle.ConstantTimeCompare(user.WebAuthnHandle, handle) == 1 {
			return user
		}
	}
	return nil
}

func (s *userAccountStore) findUserByName(name string) *userAccount {
	seed, ok := seededAccountForName(name)
	if !ok {
		return nil
	}
	return s.findUser(seed.Email)
}

// bcrypt hash of an arbitrary password, computed once, so authenticate can
// burn comparable time on unknown emails instead of returning instantly.
var unknownAccountHash, _ = bcrypt.GenerateFromPassword([]byte("bonfire-no-such-account"), bcrypt.DefaultCost)

func (s *userAccountStore) authenticate(email, password string) (*userAccount, bool) {
	user := s.findUser(email)
	if user == nil {
		_ = bcrypt.CompareHashAndPassword(unknownAccountHash, []byte(password))
		return nil, false
	}
	if bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)) != nil {
		return nil, false
	}
	// The password compare runs first so a disabled account costs the same
	// time as a wrong password: the caller's uniform "don't match" message
	// enumerates nothing.
	if user.disabled() {
		return nil, false
	}
	return user, true
}

func (s *userAccountStore) authenticateRosterName(name, password string) (*userAccount, bool) {
	user := s.findUserByName(name)
	if user == nil {
		_ = bcrypt.CompareHashAndPassword(unknownAccountHash, []byte(password))
		return nil, false
	}
	if bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password)) != nil {
		return nil, false
	}
	if user.disabled() {
		return nil, false
	}
	return user, true
}

// setDisabled stamps or clears DisabledAt with the mutate-persist-rollback
// discipline updateProfile uses. The record is never removed; a repeated call
// with the same state is a no-op that still reports the current account.
func (s *userAccountStore) setDisabled(email string, disabled bool, now time.Time) (*userAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[normalizeAccountEmail(email)]
	if !ok {
		return nil, errors.New("no such account")
	}
	if user.disabled() == disabled {
		return user, nil
	}
	previous := user.DisabledAt
	if disabled {
		stamp := now.UTC()
		user.DisabledAt = &stamp
	} else {
		user.DisabledAt = nil
	}
	if err := s.persistLocked(); err != nil {
		user.DisabledAt = previous
		return nil, err
	}
	return user, nil
}

func (s *userAccountStore) changePassword(email, currentPassword, newPassword string) error {
	user, ok := s.authenticate(email, currentPassword)
	if !ok {
		return errors.New("current password is incorrect")
	}
	return s.setPassword(user.Email, newPassword)
}

func (s *userAccountStore) setPassword(email, newPassword string) error {
	if len(newPassword) < minAccountPasswordLength {
		return fmt.Errorf("password must be at least %d characters", minAccountPasswordLength)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[normalizeAccountEmail(email)]
	if !ok {
		return errors.New("no such account")
	}
	user.PasswordHash = hash
	user.PasswordChangedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *userAccountStore) updateProfile(email, name, avatarDataURL string) (*userAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[normalizeAccountEmail(email)]
	if !ok {
		return nil, errors.New("no such account")
	}
	previousName := user.Name
	previousAvatarDataURL := user.AvatarDataURL
	user.Name = name
	user.AvatarDataURL = avatarDataURL
	if err := s.persistLocked(); err != nil {
		user.Name = previousName
		user.AvatarDataURL = previousAvatarDataURL
		return nil, err
	}
	return user, nil
}

// updateThemePref persists the account-level theme choice with the same
// mutate-persist-rollback discipline updateProfile uses.
func (s *userAccountStore) updateThemePref(email, theme string) (*userAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[normalizeAccountEmail(email)]
	if !ok {
		return nil, errors.New("no such account")
	}
	previous := user.ThemePref
	user.ThemePref = theme
	if err := s.persistLocked(); err != nil {
		user.ThemePref = previous
		return nil, err
	}
	return user, nil
}

func (s *userAccountStore) updateCredentials(email string, update func(*userAccount)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[normalizeAccountEmail(email)]
	if !ok {
		return errors.New("no such account")
	}
	update(user)
	return s.persistLocked()
}

func hashResetToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func (s *userAccountStore) createPasswordResetToken(email string) (string, error) {
	user := s.findUser(email)
	if user == nil {
		return "", errors.New("no such account")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	for key, record := range s.resetTokens {
		if time.Now().After(record.expires) || record.email == user.Email {
			delete(s.resetTokens, key)
		}
	}
	s.resetTokens[hashResetToken(token)] = resetTokenRecord{
		email:   user.Email,
		expires: time.Now().Add(resetTokenTTL),
	}
	return token, nil
}

func (s *userAccountStore) consumePasswordResetToken(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := hashResetToken(token)
	record, ok := s.resetTokens[key]
	if !ok {
		return "", false
	}
	delete(s.resetTokens, key)
	if time.Now().After(record.expires) {
		return "", false
	}
	return record.email, true
}

func (s *userAccountStore) expireResetTokenForTest(token string, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashResetToken(token)
	if record, ok := s.resetTokens[key]; ok {
		record.expires = expires
		s.resetTokens[key] = record
	}
}

// Package-level store: lazily loaded from the data directory (override with
// BONFIRE_USERS_PATH) and cached per path so tests with t.Setenv get isolated
// stores, mirroring archiveSecretCache.
var (
	userStoreMu    sync.Mutex
	userStoreCache = map[string]*userAccountStore{}
)

func usersFilePath() string {
	if path := strings.TrimSpace(os.Getenv("BONFIRE_USERS_PATH")); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(meetingMemoryPath()), "users.json")
}

func accountStore() *userAccountStore {
	path := usersFilePath()

	userStoreMu.Lock()
	defer userStoreMu.Unlock()
	if store, ok := userStoreCache[path]; ok {
		return store
	}

	store, err := newUserAccountStore(path)
	if err != nil {
		log.Errorf("Failed to load user store: %v; using in-memory accounts until the next restart", err)
		store = &userAccountStore{
			path:        path,
			users:       map[string]*userAccount{},
			resetTokens: map[string]resetTokenRecord{},
		}
		_ = store.seedMissingAccounts()
	}
	userStoreCache[path] = store
	return store
}
