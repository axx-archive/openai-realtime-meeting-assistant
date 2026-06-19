package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebsocketRejectsUnauthenticatedUpgrade(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		conn.Close()
		t.Fatal("expected websocket dial without a session to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 before upgrade, got %+v", resp)
	}
}

func TestWebsocketAdmitsSessionIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))

	// The handler outlives httptest.Server.Close (the websocket is hijacked),
	// so the global app is left in place rather than restored to nil — a nil
	// kanbanApp would panic the handler's deferred cleanup.
	if kanbanApp == nil {
		kanbanApp = newKanbanBoardApp()
	}

	token, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial with session cookie: %v", err)
	}
	defer conn.Close()

	// The payload name is attacker-controlled; the server must admit the
	// session identity instead.
	join := map[string]string{"event": "participant", "data": `{"name":"Tim","password":""}`}
	if err := conn.WriteJSON(join); err != nil {
		t.Fatalf("send participant event: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == "access_denied" {
			t.Fatalf("expected admission, got access_denied: %s", inner.Data)
		}
		if inner.Event != "access_granted" {
			continue
		}
		var grant struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(inner.Data, &grant); err != nil {
			t.Fatalf("decode access grant: %v", err)
		}
		if grant.Name != "AJ" {
			t.Fatalf("expected session identity AJ to be admitted, got %q", grant.Name)
		}
		return
	}
}

func TestWebsocketAdmitsAccountEmailWhenDisplayNameChanges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BONFIRE_USERS_PATH", filepath.Join(dir, "users.json"))
	t.Setenv("BONFIRE_SESSIONS_PATH", filepath.Join(dir, "sessions.json"))
	t.Setenv("MEETING_MEMORY_PATH", filepath.Join(dir, "memory.jsonl"))

	previousApp := kanbanApp
	kanbanApp = newKanbanBoardApp()
	t.Cleanup(func() {
		kanbanApp = previousApp
	})

	if _, err := accountStore().updateProfile("aj@shareability.com", "// aj", ""); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	token, err := userSessionStore().create("aj@shareability.com")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(websocketHandler))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/websocket"
	header := http.Header{}
	header.Set("Cookie", sessionCookieName+"="+token)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial with session cookie: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]string{"event": "participant", "data": `{}`}); err != nil {
		t.Fatalf("send participant event: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set deadline: %v", err)
		}
		var message websocketMessage
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		if message.Event != "kanban" {
			continue
		}
		var inner struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(message.Data), &inner); err != nil {
			t.Fatalf("decode kanban envelope: %v", err)
		}
		if inner.Event == "access_denied" {
			t.Fatalf("expected admission from account email, got access_denied: %s", inner.Data)
		}
		if inner.Event != "access_granted" {
			continue
		}
		var grant struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(inner.Data, &grant); err != nil {
			t.Fatalf("decode access grant: %v", err)
		}
		if grant.Name != "AJ" {
			t.Fatalf("expected email-derived room identity AJ, got %q", grant.Name)
		}
		return
	}
}
