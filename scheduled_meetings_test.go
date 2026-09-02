package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func decodeScheduledMeetingResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload struct {
		OK      bool           `json:"ok"`
		Meeting map[string]any `json:"meeting"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode scheduled meeting: %v body=%s", err, recorder.Body.String())
	}
	if !payload.OK || payload.Meeting == nil {
		t.Fatalf("scheduled meeting payload=%s, want ok+meeting", recorder.Body.String())
	}
	return payload.Meeting
}

func listScheduledMeetings(t *testing.T, query string, cookies []*http.Cookie) []map[string]any {
	t.Helper()
	recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodGet, "/assistant/meetings/scheduled"+query, "", cookies)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list %q status=%d body=%s", query, recorder.Code, recorder.Body.String())
	}
	var payload struct {
		OK       bool             `json:"ok"`
		Meetings []map[string]any `json:"meetings"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || !payload.OK {
		t.Fatalf("decode list: err=%v body=%s", err, recorder.Body.String())
	}
	return payload.Meetings
}

// D1: create/list/upcoming/patch/cancel, the organizer-or-manager ACL, the
// non-enumerating attendee check, and the timed .ics with the join URL.
func TestScheduledMeetingsCreateListUpcomingPatchCancelACLAndICS(t *testing.T) {
	aj := setupRoomsTestEnv(t)
	previousApp := kanbanApp
	kanbanApp = newIsolatedKanbanBoardApp(t)
	t.Cleanup(func() { kanbanApp = previousApp })
	room, err := appRoomStore().create("War Room", "", "aj@shareability.com", false)
	if err != nil {
		t.Fatal(err)
	}
	tim := loginAs(t, "tim@shareability.com", "B0NFIRE!")
	now := time.Now().UTC()
	soon := now.Add(72 * time.Hour).Truncate(time.Second)
	far := now.Add(30 * 24 * time.Hour).Truncate(time.Second)

	create := func(cookies []*http.Cookie, title string, startsAt time.Time, attendees string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"roomId":%q,"title":%q,"startsAt":%q,"durationMinutes":45,"attendees":%s}`, room.ID, title, startsAt.Format(time.RFC3339), attendees)
		return doRoomsRequest(t, scheduledMeetingsHandler, http.MethodPost, "/assistant/meetings/scheduled", body, cookies)
	}

	// Signed-out requests fail closed.
	if recorder := create(nil, "Launch sync", soon, `[]`); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out create status=%d, want 401", recorder.Code)
	}
	// Attendees must be registered accounts; the answer never names the address.
	recorder := create(aj, "Launch sync", soon, `["nobody@example.com"]`)
	if recorder.Code != http.StatusBadRequest || strings.Contains(recorder.Body.String(), "nobody@example.com") {
		t.Fatalf("unknown attendee status=%d body=%s, want generic 400", recorder.Code, recorder.Body.String())
	}
	// Unknown room → 404; bad start → 400.
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodPost, "/assistant/meetings/scheduled", `{"roomId":"room-missing","title":"x","startsAt":"2026-09-10T15:00:00Z"}`, aj); recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown room status=%d, want 404", recorder.Code)
	}
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodPost, "/assistant/meetings/scheduled", fmt.Sprintf(`{"roomId":%q,"title":"x","startsAt":"next tuesday"}`, room.ID), aj); recorder.Code != http.StatusBadRequest {
		t.Fatalf("bad start status=%d, want 400", recorder.Code)
	}

	recorder = create(aj, "Launch sync", soon, `["tim@shareability.com","TIM@shareability.com"]`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s, want 201", recorder.Code, recorder.Body.String())
	}
	launch := decodeScheduledMeetingResponse(t, recorder)
	launchID, _ := launch["id"].(string)
	if !strings.HasPrefix(launchID, "sched-") {
		t.Fatalf("id=%q, want sched- prefix", launchID)
	}
	if got := fmt.Sprint(launch["attendees"]); got != "[aj@shareability.com tim@shareability.com]" {
		t.Fatalf("attendees=%s, want organizer first then deduped tim", got)
	}
	if launch["joinPath"] != "/?room="+room.ID || launch["roomName"] != "War Room" || launch["endsAt"] != soon.Add(45*time.Minute).Format(time.RFC3339) || launch["cancelled"] != false {
		t.Fatalf("payload=%v, want join path, room name, derived end, not cancelled", launch)
	}
	if recorder := create(aj, "Offsite planning", far, `[]`); recorder.Code != http.StatusCreated {
		t.Fatalf("create far status=%d", recorder.Code)
	}
	recorder = create(tim, "Cancelled one", soon.Add(time.Hour), `[]`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create by tim status=%d", recorder.Code)
	}
	cancelledID, _ := decodeScheduledMeetingResponse(t, recorder)["id"].(string)

	// ACL: tim is neither organizer nor room manager of Launch sync.
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodPatch, "/assistant/meetings/scheduled/"+launchID, `{"title":"hijack"}`, tim); recorder.Code != http.StatusForbidden {
		t.Fatalf("non-organizer patch status=%d, want 403", recorder.Code)
	}
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodDelete, "/assistant/meetings/scheduled/"+launchID, "", tim); recorder.Code != http.StatusForbidden {
		t.Fatalf("non-organizer cancel status=%d, want 403", recorder.Code)
	}
	// The room manager (aj created the room) may cancel tim's meeting.
	recorder = doRoomsRequest(t, scheduledMeetingsHandler, http.MethodDelete, "/assistant/meetings/scheduled/"+cancelledID, "", aj)
	if recorder.Code != http.StatusOK || decodeScheduledMeetingResponse(t, recorder)["cancelled"] != true {
		t.Fatalf("manager cancel status=%d body=%s, want cancelled", recorder.Code, recorder.Body.String())
	}
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodPatch, "/assistant/meetings/scheduled/"+cancelledID, `{"title":"revive"}`, tim); recorder.Code != http.StatusConflict {
		t.Fatalf("patch cancelled status=%d, want 409", recorder.Code)
	}
	// Organizer edits.
	recorder = doRoomsRequest(t, scheduledMeetingsHandler, http.MethodPatch, "/assistant/meetings/scheduled/"+launchID, `{"title":"Launch sync v2","durationMinutes":30}`, aj)
	if recorder.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if patched := decodeScheduledMeetingResponse(t, recorder); patched["title"] != "Launch sync v2" || patched["durationMinutes"] != float64(30) {
		t.Fatalf("patched=%v", patched)
	}
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodGet, "/assistant/meetings/scheduled/"+launchID, "", tim); recorder.Code != http.StatusOK {
		t.Fatalf("get status=%d", recorder.Code)
	}
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodGet, "/assistant/meetings/scheduled/sched-missing", "", tim); recorder.Code != http.StatusNotFound {
		t.Fatalf("get missing status=%d", recorder.Code)
	}

	// Lists: upcoming is the next 14 days, sorted, without cancellations.
	upcoming := listScheduledMeetings(t, "?upcoming=1", tim)
	if len(upcoming) != 1 || upcoming[0]["id"] != launchID {
		t.Fatalf("upcoming=%v, want only the 72h meeting", upcoming)
	}
	all := listScheduledMeetings(t, "", tim)
	if len(all) != 2 || all[0]["id"] != launchID || all[1]["title"] != "Offsite planning" {
		t.Fatalf("all=%v, want two live meetings sorted by start", all)
	}
	if withCancelled := listScheduledMeetings(t, "?includeCancelled=1", tim); len(withCancelled) != 3 {
		t.Fatalf("includeCancelled=%d, want 3", len(withCancelled))
	}
	// Durable across a store reopen.
	if reopened := newScheduledMeetingStore(scheduledMeetingsPath()); len(reopened.list("", now, false, true)) != 3 {
		t.Fatalf("reopened store lost meetings: %d", len(reopened.list("", now, false, true)))
	}

	// Timed .ics for one meeting: UTC DTSTART/DTEND, join URL, attendees, stable UID.
	recorder = doRoomsRequest(t, calendarMeetingsICSHandler, http.MethodGet, "/calendar/meetings.ics?id="+launchID, "", aj)
	if recorder.Code != http.StatusOK || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("ics status=%d type=%q body=%s", recorder.Code, recorder.Header().Get("Content-Type"), recorder.Body.String())
	}
	unfolded := strings.ReplaceAll(recorder.Body.String(), "\r\n ", "")
	for _, want := range []string{
		"METHOD:PUBLISH\r\n",
		"DTSTART:" + soon.Format("20060102T150405Z") + "\r\n",
		"DTEND:" + soon.Add(30*time.Minute).Format("20060102T150405Z") + "\r\n",
		"SUMMARY:Launch sync v2\r\n",
		"https://bonfire.test/?room=" + room.ID,
		"URL:https://bonfire.test/?room=" + room.ID + "\r\n",
		// CN is a parameter value: quoted-string, never TEXT-escaped.
		"ATTENDEE;CN=\"tim@shareability.com\";ROLE=REQ-PARTICIPANT:mailto:tim@shareability.com\r\n",
		"ORGANIZER;CN=\"AJ\":mailto:aj@shareability.com\r\n",
		"UID:scheduled-" + launchID + "@thebonfire.xyz\r\n",
		// One edit so far: the record revision is the SEQUENCE.
		"SEQUENCE:1\r\n",
		"STATUS:CONFIRMED\r\n",
		"TRANSP:OPAQUE\r\n",
	} {
		if !strings.Contains(unfolded, want) {
			t.Fatalf("ICS missing %q in:\n%s", want, unfolded)
		}
	}
	if strings.Contains(unfolded, "VALUE=DATE") {
		t.Fatalf("scheduled meeting rendered as an all-day event:\n%s", unfolded)
	}
	if launchRecord, ok := appScheduledMeetingStore().byID(launchID); !ok || launchRecord.Revision != 1 {
		t.Fatalf("launch revision=%d ok=%t, want 1 after one edit", launchRecord.Revision, ok)
	}
	// Another edit bumps SEQUENCE so calendars re-import the change.
	if recorder := doRoomsRequest(t, scheduledMeetingsHandler, http.MethodPatch, "/assistant/meetings/scheduled/"+launchID, `{"title":"Launch sync v3"}`, aj); recorder.Code != http.StatusOK || decodeScheduledMeetingResponse(t, recorder)["revision"] != float64(2) {
		t.Fatalf("second patch status=%d body=%s, want revision 2", recorder.Code, recorder.Body.String())
	}
	recorder = doRoomsRequest(t, calendarMeetingsICSHandler, http.MethodGet, "/calendar/meetings.ics?id="+launchID, "", aj)
	if edited := strings.ReplaceAll(recorder.Body.String(), "\r\n ", ""); recorder.Code != http.StatusOK || !strings.Contains(edited, "SEQUENCE:2\r\n") || !strings.Contains(edited, "SUMMARY:Launch sync v3\r\n") || !strings.Contains(edited, "UID:scheduled-"+launchID+"@thebonfire.xyz\r\n") {
		t.Fatalf("edited ics status=%d body=%s, want SEQUENCE:2 on the same UID", recorder.Code, edited)
	}
	// The upcoming feed carries only the 14-day window and no cancellations.
	recorder = doRoomsRequest(t, calendarMeetingsICSHandler, http.MethodGet, "/calendar/meetings.ics", "", aj)
	feed := strings.ReplaceAll(recorder.Body.String(), "\r\n ", "")
	if recorder.Code != http.StatusOK || !strings.Contains(feed, "METHOD:PUBLISH\r\n") || !strings.Contains(feed, "UID:scheduled-"+launchID+"@") || strings.Contains(feed, "Offsite planning") || strings.Contains(feed, "UID:scheduled-"+cancelledID+"@") {
		t.Fatalf("feed status=%d body=%s", recorder.Code, feed)
	}
	// A cancelled meeting is served as an iTIP cancel on the same UID (not
	// 404) so a calendar that imported it retracts the entry.
	recorder = doRoomsRequest(t, calendarMeetingsICSHandler, http.MethodGet, "/calendar/meetings.ics?id="+cancelledID, "", aj)
	cancelledICS := strings.ReplaceAll(recorder.Body.String(), "\r\n ", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("cancelled ics status=%d body=%s, want 200 METHOD:CANCEL", recorder.Code, cancelledICS)
	}
	for _, want := range []string{
		"METHOD:CANCEL\r\n",
		"STATUS:CANCELLED\r\n",
		"SEQUENCE:1\r\n",
		"UID:scheduled-" + cancelledID + "@thebonfire.xyz\r\n",
		"ORGANIZER;CN=\"Tim\":mailto:tim@shareability.com\r\n",
	} {
		if !strings.Contains(cancelledICS, want) {
			t.Fatalf("cancelled ICS missing %q in:\n%s", want, cancelledICS)
		}
	}
	if strings.Contains(cancelledICS, "STATUS:CONFIRMED") || strings.Contains(cancelledICS, "METHOD:PUBLISH") {
		t.Fatalf("cancelled ICS still publishes a live event:\n%s", cancelledICS)
	}
	if recorder := doRoomsRequest(t, calendarMeetingsICSHandler, http.MethodGet, "/calendar/meetings.ics?id=sched-missing", "", aj); recorder.Code != http.StatusNotFound {
		t.Fatalf("missing ics status=%d, want 404", recorder.Code)
	}
	if recorder := doRoomsRequest(t, calendarMeetingsICSHandler, http.MethodGet, "/calendar/meetings.ics", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out ics status=%d, want 401", recorder.Code)
	}
	if !calendarCapabilities()["scheduled"].(bool) {
		t.Fatal("client-config calendar capabilities must advertise scheduled meetings")
	}
}

// The all-day path is untouched (TestBuildICSCalendar); a timed event renders
// DTSTART/DTEND with the Z suffix and honors the caller-pinned UID.
func TestBuildICSCalendarTimedEvent(t *testing.T) {
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	start := time.Date(2026, time.September, 4, 15, 30, 0, 0, time.UTC)
	event := icsEvent{
		Title: "Design, review", Start: start, End: start.Add(45 * time.Minute),
		Description: "Join: https://bonfire.test/?room=office", Attendees: []string{"AJ@shareability.com", ""},
		UID: "scheduled-sched-1@thebonfire.xyz", URL: "https://bonfire.test/?room=office",
		Sequence: 3, Organizer: "AJ@shareability.com", OrganizerName: "A \"J\"\r\nHart; Jr,",
	}
	out := strings.ReplaceAll(string(buildICSCalendar([]icsEvent{event}, now)), "\r\n ", "")
	for _, want := range []string{
		"METHOD:PUBLISH\r\n",
		"DTSTART:20260904T153000Z\r\n", "DTEND:20260904T161500Z\r\n", "SUMMARY:Design\\, review\r\n",
		"DESCRIPTION:Join: https://bonfire.test/?room=office\r\n", "URL:https://bonfire.test/?room=office\r\n",
		// Param values are quoted-strings: DQUOTE/CR/LF stripped, ; and , kept
		// verbatim inside the quotes (no TEXT escaping).
		"ATTENDEE;CN=\"aj@shareability.com\";ROLE=REQ-PARTICIPANT:mailto:aj@shareability.com\r\n",
		"ORGANIZER;CN=\"A JHart; Jr,\":mailto:aj@shareability.com\r\n",
		"UID:scheduled-sched-1@thebonfire.xyz\r\n",
		"SEQUENCE:3\r\n",
		"STATUS:CONFIRMED\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("timed ICS missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "VALUE=DATE") || strings.Count(out, "ATTENDEE") != 1 || strings.Count(out, "ORGANIZER") != 1 {
		t.Fatalf("timed ICS shape wrong:\n%s", out)
	}
	if again := strings.ReplaceAll(string(buildICSCalendar([]icsEvent{event}, now)), "\r\n ", ""); again != out {
		t.Fatal("timed ICS is not deterministic")
	}
	// No organizer → no ORGANIZER line; a negative sequence clamps to 0.
	bare := event
	bare.Organizer, bare.OrganizerName, bare.Sequence = "", "", -4
	if bareOut := string(buildICSCalendar([]icsEvent{bare}, now)); strings.Contains(bareOut, "ORGANIZER") || !strings.Contains(bareOut, "SEQUENCE:0\r\n") {
		t.Fatalf("bare timed ICS wrong:\n%s", bareOut)
	}
	// A cancellation rides METHOD:CANCEL with STATUS:CANCELLED on the same UID.
	cancelled := event
	cancelled.Cancelled, cancelled.Sequence = true, 4
	cancelledOut := strings.ReplaceAll(string(buildICSCalendarWithMethod([]icsEvent{cancelled}, now, icsMethodCancel)), "\r\n ", "")
	for _, want := range []string{"METHOD:CANCEL\r\n", "STATUS:CANCELLED\r\n", "SEQUENCE:4\r\n", "UID:scheduled-sched-1@thebonfire.xyz\r\n"} {
		if !strings.Contains(cancelledOut, want) {
			t.Fatalf("cancel ICS missing %q in:\n%s", want, cancelledOut)
		}
	}
	if strings.Contains(cancelledOut, "STATUS:CONFIRMED") || strings.Contains(cancelledOut, "METHOD:PUBLISH") {
		t.Fatalf("cancel ICS still confirms:\n%s", cancelledOut)
	}
	for raw, want := range map[string]string{
		`plain`:            `"plain"`,
		`  padded  `:       `"padded"`,
		"quo\"te\r\nline":  `"quoteline"`,
		"tab\tctl\x7fdone": `"tabctldone"`,
	} {
		if got := icsParamValue(raw); got != want {
			t.Fatalf("icsParamValue(%q)=%s, want %s", raw, got, want)
		}
	}
}
