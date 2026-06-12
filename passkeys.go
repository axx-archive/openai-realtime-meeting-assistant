package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	webauthnCeremonyCookieName = "bonfire_webauthn"
	webauthnCeremonyTTL        = 5 * time.Minute
)

// webAuthnForRequest builds a relying party scoped to the host actually being
// browsed (thebonfire.xyz in production, localhost during smoke tests), so
// passkeys bind to whichever origin served the page.
func webAuthnForRequest(r *http.Request) (*webauthn.WebAuthn, error) {
	host := r.Host
	if splitHost, _, err := net.SplitHostPort(r.Host); err == nil {
		host = splitHost
	}

	scheme := "http"
	if requestIsSecure(r) {
		scheme = "https"
	}

	return webauthn.New(&webauthn.Config{
		RPID:          host,
		RPDisplayName: "Bonfire",
		RPOrigins:     []string{scheme + "://" + r.Host},
	})
}

type webauthnCeremony struct {
	email   string
	session *webauthn.SessionData
	expires time.Time
}

var (
	webauthnCeremonyMu sync.Mutex
	webauthnCeremonies = map[string]webauthnCeremony{}
)

func storeWebauthnCeremony(w http.ResponseWriter, r *http.Request, email string, session *webauthn.SessionData) error {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	id := hex.EncodeToString(raw)

	webauthnCeremonyMu.Lock()
	for key, ceremony := range webauthnCeremonies {
		if time.Now().After(ceremony.expires) {
			delete(webauthnCeremonies, key)
		}
	}
	webauthnCeremonies[id] = webauthnCeremony{
		email:   email,
		session: session,
		expires: time.Now().Add(webauthnCeremonyTTL),
	}
	webauthnCeremonyMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     webauthnCeremonyCookieName,
		Value:    id,
		Path:     "/auth/",
		MaxAge:   int(webauthnCeremonyTTL / time.Second),
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func takeWebauthnCeremony(r *http.Request) (*webauthnCeremony, error) {
	cookie, err := r.Cookie(webauthnCeremonyCookieName)
	if err != nil || cookie.Value == "" {
		return nil, errors.New("no passkey ceremony in progress")
	}

	webauthnCeremonyMu.Lock()
	defer webauthnCeremonyMu.Unlock()
	ceremony, ok := webauthnCeremonies[cookie.Value]
	delete(webauthnCeremonies, cookie.Value)
	if !ok || time.Now().After(ceremony.expires) {
		return nil, errors.New("the passkey ceremony expired; try again")
	}
	return &ceremony, nil
}

func passkeyID(credential webauthn.Credential) string {
	return base64.RawURLEncoding.EncodeToString(credential.ID)
}

func passkeyListPayload(user *userAccount) map[string]any {
	passkeys := make([]map[string]any, 0, len(user.Credentials))
	for index, credential := range user.Credentials {
		label := fmt.Sprintf("passkey %d", index+1)
		if added, ok := user.PasskeyAddedAt[passkeyID(credential)]; ok {
			label = "passkey added " + added.Format("Jan 2, 2006")
		}
		passkeys = append(passkeys, map[string]any{
			"id":    passkeyID(credential),
			"label": label,
		})
	}
	return map[string]any{"passkeys": passkeys}
}

func handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	relyingParty, err := webAuthnForRequest(r)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "passkeys are unavailable")
		return
	}

	options, session, err := relyingParty.BeginRegistration(
		user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthn.WithExclusions(user.credentialDescriptors()),
	)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start passkey registration")
		return
	}
	if err := storeWebauthnCeremony(w, r, user.Email, session); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start passkey registration")
		return
	}
	writeAuthJSON(w, http.StatusOK, options)
}

func handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	ceremony, err := takeWebauthnCeremony(r)
	if err != nil || !strings.EqualFold(ceremony.email, user.Email) {
		writeAuthError(w, http.StatusBadRequest, "no passkey ceremony in progress")
		return
	}

	relyingParty, err := webAuthnForRequest(r)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "passkeys are unavailable")
		return
	}

	credential, err := relyingParty.FinishRegistration(user, *ceremony.session, r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "passkey registration failed")
		return
	}

	if err := accountStore().updateCredentials(user.Email, func(account *userAccount) {
		account.Credentials = append(account.Credentials, *credential)
		if account.PasskeyAddedAt == nil {
			account.PasskeyAddedAt = map[string]time.Time{}
		}
		account.PasskeyAddedAt[passkeyID(*credential)] = time.Now().UTC()
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not save the passkey")
		return
	}
	writeAuthJSON(w, http.StatusOK, passkeyListPayload(accountStore().findUser(user.Email)))
}

func handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !authAttemptAllowed("passkey-login", r) {
		writeAuthError(w, http.StatusTooManyRequests, "too many sign-in attempts; try again in a few minutes")
		return
	}

	relyingParty, err := webAuthnForRequest(r)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "passkeys are unavailable")
		return
	}

	options, session, err := relyingParty.BeginDiscoverableLogin()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start passkey sign-in")
		return
	}
	if err := storeWebauthnCeremony(w, r, "", session); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start passkey sign-in")
		return
	}
	writeAuthJSON(w, http.StatusOK, options)
}

func handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	ceremony, err := takeWebauthnCeremony(r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}

	relyingParty, err := webAuthnForRequest(r)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "passkeys are unavailable")
		return
	}

	var matchedUser *userAccount
	credential, err := relyingParty.FinishDiscoverableLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		user := accountStore().findUserByWebAuthnHandle(userHandle)
		if user == nil {
			return nil, errors.New("unknown passkey")
		}
		matchedUser = user
		return user, nil
	}, *ceremony.session, r)
	if err != nil || matchedUser == nil {
		writeAuthError(w, http.StatusUnauthorized, "passkey sign-in failed")
		return
	}

	// Persist the updated signature counter / backup flags.
	_ = accountStore().updateCredentials(matchedUser.Email, func(account *userAccount) {
		for index := range account.Credentials {
			if passkeyID(account.Credentials[index]) == passkeyID(*credential) {
				account.Credentials[index] = *credential
			}
		}
	})

	token, err := userSessionStore().create(matchedUser.Email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not start a session")
		return
	}
	setSessionCookie(w, r, token, int(sessionTTL/time.Second))
	writeAuthJSON(w, http.StatusOK, identityPayload(matchedUser))
}

func handlePasskeyList(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}
	writeAuthJSON(w, http.StatusOK, passkeyListPayload(user))
}

func handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	user := userFromRequest(r)
	if user == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	payload := struct {
		ID string `json:"id"`
	}{}
	if err := decodeAuthBody(r, &payload); err != nil || payload.ID == "" {
		writeAuthError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	if err := accountStore().updateCredentials(user.Email, func(account *userAccount) {
		kept := account.Credentials[:0]
		for _, credential := range account.Credentials {
			if passkeyID(credential) != payload.ID {
				kept = append(kept, credential)
			}
		}
		account.Credentials = kept
		delete(account.PasskeyAddedAt, payload.ID)
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not remove the passkey")
		return
	}
	writeAuthJSON(w, http.StatusOK, passkeyListPayload(accountStore().findUser(user.Email)))
}
