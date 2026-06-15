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

// seededAccounts is the complete roster of Bonfire accounts. There is no
// signup path: accounts exist only because they are listed here, and seeding
// never touches an account that already exists in the store file.
var seededAccounts = []struct {
	Email string
	Name  string
}{
	{"aj@shareability.com", "AJ"},
	{"tim@shareability.com", "Tim"},
	{"e@shareability.com", "Erick"},
	{"joel@shareability.com", "Joel"},
	{"tyler@shareability.com", "Tyler"},
	{"caitlyn@shareability.com", "Caitlyn"},
	{"tom@shareability.com", "Tom"},
}

type userAccount struct {
	Email             string                `json:"email"`
	Name              string                `json:"name"`
	PasswordHash      []byte                `json:"passwordHash"`
	WebAuthnHandle    []byte                `json:"webauthnHandle"`
	Credentials       []webauthn.Credential `json:"credentials,omitempty"`
	PasskeyAddedAt    map[string]time.Time  `json:"passkeyAddedAt,omitempty"`
	PasswordChangedAt time.Time             `json:"passwordChangedAt"`
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
			store.users[normalizeAccountEmail(user.Email)] = user
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

	seeded := false
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
		seeded = true
	}

	if !seeded {
		return nil
	}
	return s.persistLocked()
}

func (s *userAccountStore) persistLocked() error {
	accounts := make([]*userAccount, 0, len(s.users))
	for _, seed := range seededAccounts {
		if user, ok := s.users[normalizeAccountEmail(seed.Email)]; ok {
			accounts = append(accounts, user)
		}
	}

	raw, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	// Write-then-rename so a crash mid-write cannot truncate the only copy of
	// everyone's password hashes and passkeys.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *userAccountStore) accountEmails() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	emails := make([]string, 0, len(s.users))
	for _, seed := range seededAccounts {
		if user, ok := s.users[normalizeAccountEmail(seed.Email)]; ok {
			emails = append(emails, user.Email)
		}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if strings.EqualFold(user.Name, strings.TrimSpace(name)) {
			return user
		}
	}
	return nil
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
	return user, true
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
