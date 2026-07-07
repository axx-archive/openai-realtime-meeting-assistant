package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Card 084: the fuzzy key-date parser. Free-text months (any case), ISO,
// slash forms, explicit years, and the yearless roll-forward all resolve to a
// concrete UTC all-day date; anything it can't read returns false so the
// handler answers 400 → toast.
func TestParseKeyDateString(t *testing.T) {
	// A fixed "now" so the yearless roll-forward is deterministic.
	nowMid := time.Date(2026, time.May, 20, 9, 0, 0, 0, time.UTC)
	nowJul := time.Date(2026, time.July, 6, 9, 0, 0, 0, time.UTC)
	nowDec := time.Date(2026, time.December, 20, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		raw  string
		now  time.Time
		want string // YYYYMMDD, or "" for expected-false
	}{
		{"iso", "2026-01-02", nowJul, "20260102"},
		{"explicit-year-abbrev", "May 24, 2026", nowJul, "20260524"},
		{"explicit-year-no-comma", "May 24 2026", nowJul, "20260524"},
		{"explicit-year-full-month", "January 2, 2027", nowJul, "20270102"},
		{"slash-with-year", "5/24/2026", nowJul, "20260524"},
		{"lowercase-month", "may 24, 2026", nowJul, "20260524"},
		{"uppercase-month", "MAY 24, 2026", nowJul, "20260524"},
		{"yearless-near-now-stays-this-year", "May 24", nowMid, "20260524"},
		{"yearless-well-past-rolls-forward", "May 24", nowJul, "20270524"},
		{"yearless-across-year-boundary", "May 24", nowDec, "20270524"},
		{"yearless-slash", "5/24", nowMid, "20260524"},
		{"whitespace-padded", "  May   24, 2026 ", nowJul, "20260524"},
		{"garbage-free-text", "after the offsite", nowJul, ""},
		{"empty", "", nowJul, ""},
		{"quarter-phrase", "end of Q3", nowJul, ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := parseKeyDateString(testCase.raw, testCase.now)
			if testCase.want == "" {
				if ok {
					t.Fatalf("parseKeyDateString(%q) = %s, want unparseable", testCase.raw, got.Format("20060102"))
				}
				return
			}
			if !ok {
				t.Fatalf("parseKeyDateString(%q) failed, want %s", testCase.raw, testCase.want)
			}
			if stamp := got.Format("20060102"); stamp != testCase.want {
				t.Fatalf("parseKeyDateString(%q) = %s, want %s", testCase.raw, stamp, testCase.want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("parseKeyDateString(%q) location = %v, want UTC", testCase.raw, got.Location())
			}
		})
	}
}

// buildICSCalendar renders a well-formed, CRLF-terminated, escaped, all-day
// VEVENT with a deterministic UID.
func TestBuildICSCalendar(t *testing.T) {
	now := time.Date(2026, time.May, 1, 12, 30, 0, 0, time.UTC)
	date := time.Date(2026, time.May, 24, 0, 0, 0, 0, time.UTC)
	event := icsEvent{Title: "A,B", Label: "c;d\ne", Date: date}

	out := string(buildICSCalendar([]icsEvent{event}, now))

	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n",
		"VERSION:2.0\r\n",
		"PRODID:-//Bonfire//Meeting OS//EN\r\n",
		"BEGIN:VEVENT\r\n",
		"DTSTAMP:20260501T123000Z\r\n",
		"DTSTART;VALUE=DATE:20260524\r\n",
		"DTEND;VALUE=DATE:20260525\r\n",
		// title + label joined, with RFC-5545 escaping of , ; and newline
		"SUMMARY:A\\,B: c\\;d\\ne\r\n",
		"END:VEVENT\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("ICS missing %q in:\n%s", want, out)
		}
	}

	// The UID line exceeds 75 octets and folds; unfolded (CRLF+space removed
	// per RFC 5545 §3.1) it carries the deterministic @thebonfire.xyz UID.
	if unfolded := strings.ReplaceAll(out, "\r\n ", ""); !strings.Contains(unfolded, "UID:") || !strings.Contains(unfolded, "@thebonfire.xyz") {
		t.Fatalf("ICS missing a folded UID@thebonfire.xyz in:\n%s", out)
	}

	// Every newline is a CRLF terminator — no bare LF leaks (escaped newlines
	// became the literal two-character sequence backslash-n).
	for index := 0; index < len(out); index++ {
		if out[index] == '\n' && (index == 0 || out[index-1] != '\r') {
			t.Fatalf("bare LF at byte %d, want CRLF line endings only", index)
		}
	}

	// Deterministic UID: same event, same bytes.
	again := string(buildICSCalendar([]icsEvent{event}, now))
	if again != out {
		t.Fatal("buildICSCalendar is not deterministic for identical input")
	}
}

// The route mirrors artifactBlobHandler's guard contract and serves a
// text/calendar attachment on the happy path.
func TestCalendarICSHandler(t *testing.T) {
	setupAuthTestEnv(t)

	// Method gate.
	recorder := httptest.NewRecorder()
	calendarICSHandler(recorder, httptest.NewRequest(http.MethodPost, "/calendar/event.ics", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want 405", recorder.Code)
	}

	// Session gate: no cookie → 401.
	recorder = httptest.NewRecorder()
	calendarICSHandler(recorder, httptest.NewRequest(http.MethodGet, "/calendar/event.ics?date=2026-05-24", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("signed-out status=%d, want 401", recorder.Code)
	}

	cookies := loginAs(t, "aj@shareability.com", "B0NFIRE!")
	get := func(target string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		calendarICSHandler(rec, req)
		return rec
	}

	// Unparseable date → 400.
	if rec := get("/calendar/event.ics?title=Ship&label=review&date=after%20the%20offsite"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unparseable-date status=%d, want 400", rec.Code)
	}

	// Happy path.
	rec := get("/calendar/event.ics?title=" + url.QueryEscape("Ship the OS") + "&label=" + url.QueryEscape("design review") + "&date=" + url.QueryEscape("May 24, 2026"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/calendar; charset=utf-8" {
		t.Fatalf("Content-Type=%q, want text/calendar", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q, want nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disposition, "attachment;") || !strings.Contains(disposition, ".ics\"") {
		t.Fatalf("Content-Disposition=%q, want an .ics attachment", disposition)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "BEGIN:VCALENDAR") || !strings.Contains(body, "DTSTART;VALUE=DATE:20260524") {
		t.Fatalf("body missing calendar structure:\n%s", body)
	}
}

// The Google OAuth seam only reports configured when all three creds are set —
// a half-set env never advertises a broken button.
func TestGoogleCalendarConfigFromEnv(t *testing.T) {
	t.Setenv("GOOGLE_CALENDAR_CLIENT_ID", "")
	t.Setenv("GOOGLE_CALENDAR_CLIENT_SECRET", "")
	t.Setenv("GOOGLE_CALENDAR_REDIRECT_URL", "")
	if googleCalendarConfigFromEnv().configured() {
		t.Fatal("empty env should not be configured")
	}
	if caps := calendarCapabilities(); caps["ics"] != true || caps["google"] != false {
		t.Fatalf("capabilities=%v, want ics:true google:false", caps)
	}

	// Two of three set is still not configured.
	t.Setenv("GOOGLE_CALENDAR_CLIENT_ID", "id")
	t.Setenv("GOOGLE_CALENDAR_CLIENT_SECRET", "secret")
	if googleCalendarConfigFromEnv().configured() {
		t.Fatal("partial env should not be configured")
	}

	t.Setenv("GOOGLE_CALENDAR_REDIRECT_URL", "https://bonfire.test/calendar/google/callback")
	if !googleCalendarConfigFromEnv().configured() {
		t.Fatal("all three creds set should be configured")
	}
	if caps := calendarCapabilities(); caps["google"] != true {
		t.Fatalf("capabilities=%v, want google:true once creds exist", caps)
	}
}
