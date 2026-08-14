package main

import "net/http"

// assistantBoardCardsHandler preserves the authenticated legacy route while
// making Board history immutable. New work lives in conversation-backed Work.
func assistantBoardCardsHandler(w http.ResponseWriter, r *http.Request) {
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
		writeAuthError(w, http.StatusServiceUnavailable, "the board is unavailable")
		return
	}
	writeAuthError(w, http.StatusGone, ErrBoardRetired.Error())
}
