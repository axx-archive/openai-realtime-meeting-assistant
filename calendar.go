package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Card 084 (calendar, .ics slice): turn any card key date — the repo's only
// structured "proposed time" object, captured live by the add_key_date /
// update_ticket voice tools — into a downloadable RFC 5545 all-day event.
// Stateless by design: everything the .ics needs rides in the query string, so
// the endpoint also serves the frontend's localStorage-only date overrides
// without touching the board lock. The Google OAuth plug-in point is reserved
// behind env vars (googleCalendarConfigFromEnv) and advertised through
// /client-config; no live Google API calls exist yet — a future wave adds
// /calendar/google/* handlers that consume the same icsEvent struct.

// googleCalendarConfig reserves the Google Calendar OAuth seam behind env vars,
// mirroring meetingNotesSMTPConfig. Nothing consumes it today; its only job is
// to let /client-config report whether Google sync is wired.
type googleCalendarConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func googleCalendarConfigFromEnv() googleCalendarConfig {
	return googleCalendarConfig{
		ClientID:     strings.TrimSpace(os.Getenv("GOOGLE_CALENDAR_CLIENT_ID")),
		ClientSecret: strings.TrimSpace(os.Getenv("GOOGLE_CALENDAR_CLIENT_SECRET")),
		RedirectURL:  strings.TrimSpace(os.Getenv("GOOGLE_CALENDAR_REDIRECT_URL")),
	}
}

// configured reports whether every credential the future OAuth flow needs is
// present — all three or nothing, so a half-set env never advertises a broken
// Google button.
func (config googleCalendarConfig) configured() bool {
	return config.ClientID != "" && config.ClientSecret != "" && config.RedirectURL != ""
}

// calendarCapabilities is merged into the session-gated /client-config so the
// frontend can light up the right affordances: the .ics download always works
// (stateless, no creds), Google sync only once OAuth creds exist.
func calendarCapabilities() map[string]any {
	return map[string]any{
		"ics":       true,
		"scheduled": true,
		"google":    googleCalendarConfigFromEnv().configured(),
	}
}

// calendarKeyDateGraceDays: a yearless key date ("May 24") whose month/day is
// more than this many days in the past for the current year is assumed to mean
// next year (a "May 24" referenced in December). Small enough that a date a few
// days stale still resolves to this year — people hit "add to calendar" shortly
// after a date is set — large enough to absorb clock skew.
const calendarKeyDateGraceDays = 14

// keyDateLayouts are tried in order after case-normalization. Go's time.Parse
// is case-sensitive on month names and distinguishes "Jan" from "January", so
// both the abbreviated and full variants are listed; year-bearing layouts come
// first so "May 24, 2026" never loses its explicit year to a yearless match.
var keyDateLayouts = []string{
	"2006-01-02",
	"January 2, 2006",
	"Jan 2, 2006",
	"January 2 2006",
	"Jan 2 2006",
	"1/2/2006",
	"January 2",
	"Jan 2",
	"1/2",
}

// normalizeKeyDateCase title-cases every whitespace token and collapses runs of
// spaces so free-text months ("MAY", "may", "  May ") match Go's case-sensitive
// month tokens. Numeric tokens ("24,", "2026", "5/24") pass through unchanged.
func normalizeKeyDateCase(value string) string {
	fields := strings.Fields(value)
	for index, field := range fields {
		runes := []rune(field)
		for position := range runes {
			if position == 0 {
				runes[position] = unicode.ToUpper(runes[position])
			} else {
				runes[position] = unicode.ToLower(runes[position])
			}
		}
		fields[index] = string(runes)
	}
	return strings.Join(fields, " ")
}

// parseKeyDateString turns a free-text key date into a concrete all-day date.
// It normalizes exactly like the storage path (normalizeKeyDateText) then case-
// folds month names before trying each layout; yearless matches roll forward to
// the nearest sensible year relative to now. Returns false for anything it can't
// resolve ("after the offsite", "end of Q3", "") so the handler can 400 → toast.
func parseKeyDateString(raw string, now time.Time) (time.Time, bool) {
	normalized := normalizeKeyDateCase(normalizeKeyDateText(raw))
	if normalized == "" {
		return time.Time{}, false
	}
	for _, layout := range keyDateLayouts {
		parsed, err := time.Parse(layout, normalized)
		if err != nil {
			continue
		}
		if !strings.Contains(layout, "2006") {
			parsed = rollForwardYearless(parsed, now)
		}
		return parsed.UTC(), true
	}
	return time.Time{}, false
}

// rollForwardYearless pins a yearless month/day (parsed with year 0000) to the
// current year, bumping to next year only if it lands more than the grace window
// in the past. All-day dates live at UTC midnight for a stable YYYYMMDD.
func rollForwardYearless(parsed time.Time, now time.Time) time.Time {
	candidate := time.Date(now.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
	graceStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -calendarKeyDateGraceDays)
	if candidate.Before(graceStart) {
		candidate = candidate.AddDate(1, 0, 0)
	}
	return candidate
}

// icsEvent is the single shape the .ics builder — and the future Google
// inserter — consume: a card title for context, the key-date label, and a
// resolved all-day date.
type icsEvent struct {
	Title string
	Label string
	Date  time.Time
	// Timed form (Wave 7 scheduled meetings): when End is set the event is
	// rendered with UTC DTSTART/DTEND instead of an all-day date, carrying the
	// join URL, one ATTENDEE per email, and a caller-pinned UID.
	Start       time.Time
	End         time.Time
	Description string
	Attendees   []string
	UID         string
	URL         string
	// Sequence is the record revision (RFC 5545 SEQUENCE): calendars only
	// apply an update or a cancel whose SEQUENCE is higher than the copy they
	// hold, so an edited appointment re-imports instead of being ignored.
	Sequence int
	// Organizer is the organizer's email; OrganizerName its CN (falls back to
	// the email). Emitted as ORGANIZER;CN="…":mailto:… on timed events.
	Organizer     string
	OrganizerName string
	// Cancelled renders STATUS:CANCELLED (the calendar is then served as
	// METHOD:CANCEL by the caller); live timed events carry STATUS:CONFIRMED.
	Cancelled bool
}

// icsParamValue renders a property parameter value (e.g. CN=) as an RFC 5545
// §3.2 quoted-string. Param values are NOT text values: TEXT escaping
// (backslash-comma) is wrong here; instead the value is double-quoted and
// anything a quoted-string may not contain — DQUOTE, CR/LF and other control
// characters — is stripped.
func icsParamValue(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, char := range strings.TrimSpace(value) {
		if char == '"' || char < 0x20 || char == 0x7f {
			continue
		}
		builder.WriteRune(char)
	}
	builder.WriteByte('"')
	return builder.String()
}

// timed reports whether the event renders as a DTSTART/DTEND appointment.
func (event icsEvent) timed() bool {
	return !event.End.IsZero()
}

// escapeICSText escapes text per RFC 5545 §3.3.11 (TEXT value type): backslash,
// semicolon, comma, and any newline (CR/LF/CRLF → literal "\n"). Backslash is
// replaced first, and strings.Replacer makes a single non-overlapping pass, so
// the escape sequences it inserts are never re-escaped.
func escapeICSText(value string) string {
	return strings.NewReplacer(
		"\\", "\\\\",
		";", "\\;",
		",", "\\,",
		"\r\n", "\\n",
		"\n", "\\n",
		"\r", "\\n",
	).Replace(value)
}

// foldICSLine folds a content line to <=75 octets with a leading-space
// continuation per RFC 5545 §3.1, cutting on UTF-8 rune boundaries so a folded
// multibyte character is never split.
func foldICSLine(line string) string {
	const limit = 73 // 75-octet cap with headroom for the CRLF that follows
	if len(line) <= limit {
		return line
	}
	var builder strings.Builder
	for len(line) > limit {
		cut := limit
		for cut > 0 && line[cut]&0xC0 == 0x80 {
			cut--
		}
		if cut == 0 { // a single rune wider than the limit — emit it whole
			cut = limit
		}
		builder.WriteString(line[:cut])
		builder.WriteString("\r\n ")
		line = line[cut:]
	}
	builder.WriteString(line)
	return builder.String()
}

// buildICSCalendar renders a CRLF-terminated VCALENDAR carrying one all-day
// VEVENT per input. UIDs are a deterministic sha256 of title|label|date so the
// same key date re-imports as the same event (calendars dedupe on UID);
// SUMMARY/DESCRIPTION are RFC-5545 escaped. now stamps DTSTAMP in UTC.
func buildICSCalendar(events []icsEvent, now time.Time) []byte {
	return buildICSCalendarWithMethod(events, now, icsMethodPublish)
}

const (
	icsMethodPublish = "PUBLISH"
	icsMethodCancel  = "CANCEL"
)

// buildICSCalendarWithMethod is buildICSCalendar with an explicit iTIP METHOD:
// PUBLISH for a feed or a live appointment, CANCEL for a cancelled scheduled
// meeting (the VEVENT keeps its UID and carries STATUS:CANCELLED + a bumped
// SEQUENCE, which is how a subscribed calendar retracts the entry).
func buildICSCalendarWithMethod(events []icsEvent, now time.Time, method string) []byte {
	var builder strings.Builder
	writeLine := func(line string) {
		builder.WriteString(foldICSLine(line))
		builder.WriteString("\r\n")
	}
	if strings.TrimSpace(method) == "" {
		method = icsMethodPublish
	}
	writeLine("BEGIN:VCALENDAR")
	writeLine("VERSION:2.0")
	writeLine("PRODID:-//Bonfire//Meeting OS//EN")
	writeLine("CALSCALE:GREGORIAN")
	writeLine("METHOD:" + method)

	stamp := now.UTC().Format("20060102T150405Z")
	for _, event := range events {
		if event.timed() {
			writeTimedICSEvent(writeLine, event, stamp)
			continue
		}
		day := event.Date.UTC()
		start := day.Format("20060102")
		end := day.AddDate(0, 0, 1).Format("20060102")

		title := strings.TrimSpace(event.Title)
		label := strings.TrimSpace(event.Label)
		summary := label
		switch {
		case title != "" && label != "":
			summary = title + ": " + label
		case summary == "":
			summary = title
		}
		if summary == "" {
			summary = "Key date"
		}

		sum := sha256.Sum256([]byte(title + "|" + label + "|" + start))
		uid := hex.EncodeToString(sum[:]) + "@thebonfire.xyz"

		writeLine("BEGIN:VEVENT")
		writeLine("UID:" + uid)
		writeLine("DTSTAMP:" + stamp)
		writeLine("DTSTART;VALUE=DATE:" + start)
		writeLine("DTEND;VALUE=DATE:" + end)
		writeLine("SUMMARY:" + escapeICSText(summary))
		writeLine("DESCRIPTION:" + escapeICSText("Key date from Bonfire (thebonfire.xyz)."))
		writeLine("TRANSP:TRANSPARENT")
		writeLine("END:VEVENT")
	}
	writeLine("END:VCALENDAR")
	return []byte(builder.String())
}

// writeTimedICSEvent renders one scheduled meeting as a timed VEVENT. Times
// are emitted in UTC (the "Z" form needs no VTIMEZONE); the UID defaults to a
// deterministic digest but callers pin a record-bound one so an edited
// appointment re-imports as the same entry. ATTENDEE lines carry mailto: with
// the address as CN — the roster's display names are not on the wire.
func writeTimedICSEvent(writeLine func(string), event icsEvent, stamp string) {
	start := event.Start.UTC()
	end := event.End.UTC()
	if !end.After(start) {
		end = start.Add(30 * time.Minute)
	}
	summary := strings.TrimSpace(event.Title)
	if summary == "" {
		summary = strings.TrimSpace(event.Label)
	}
	if summary == "" {
		summary = "Meeting"
	}
	uid := strings.TrimSpace(event.UID)
	if uid == "" {
		sum := sha256.Sum256([]byte(summary + "|" + start.Format("20060102T150405Z") + "|" + end.Format("20060102T150405Z")))
		uid = hex.EncodeToString(sum[:]) + "@thebonfire.xyz"
	}
	description := strings.TrimSpace(event.Description)
	if description == "" {
		description = "Scheduled meeting from Bonfire (thebonfire.xyz)."
	}
	sequence := event.Sequence
	if sequence < 0 {
		sequence = 0
	}
	writeLine("BEGIN:VEVENT")
	writeLine("UID:" + uid)
	writeLine("DTSTAMP:" + stamp)
	writeLine("SEQUENCE:" + strconv.Itoa(sequence))
	writeLine("DTSTART:" + start.Format("20060102T150405Z"))
	writeLine("DTEND:" + end.Format("20060102T150405Z"))
	writeLine("SUMMARY:" + escapeICSText(summary))
	writeLine("DESCRIPTION:" + escapeICSText(description))
	if event.Cancelled {
		writeLine("STATUS:CANCELLED")
	} else {
		writeLine("STATUS:CONFIRMED")
	}
	if joinURL := strings.TrimSpace(event.URL); joinURL != "" {
		writeLine("URL:" + joinURL)
		writeLine("LOCATION:" + escapeICSText(joinURL))
	}
	if organizer := normalizeAccountEmail(event.Organizer); organizer != "" {
		organizerName := strings.TrimSpace(event.OrganizerName)
		if organizerName == "" {
			organizerName = organizer
		}
		writeLine("ORGANIZER;CN=" + icsParamValue(organizerName) + ":mailto:" + organizer)
	}
	for _, attendee := range event.Attendees {
		email := normalizeAccountEmail(attendee)
		if email == "" {
			continue
		}
		writeLine("ATTENDEE;CN=" + icsParamValue(email) + ";ROLE=REQ-PARTICIPANT:mailto:" + email)
	}
	writeLine("TRANSP:OPAQUE")
	writeLine("END:VEVENT")
}

// slugForFilename reduces a caller string to a lowercase ascii-and-digit slug
// with single hyphens between runs, no leading/trailing hyphen.
func slugForFilename(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	pendingDash := false
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			if pendingDash && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			builder.WriteRune(char)
			pendingDash = false
		} else {
			pendingDash = true
		}
	}
	return builder.String()
}

// calendarICSFilename builds a "<title>-<label>.ics" name from the slugged
// inputs; blobDownloadFilename sanitizes it again at the header for defense in
// depth.
func calendarICSFilename(title, label string) string {
	parts := make([]string, 0, 2)
	if slug := slugForFilename(title); slug != "" {
		parts = append(parts, slug)
	}
	if slug := slugForFilename(label); slug != "" {
		parts = append(parts, slug)
	}
	base := strings.Join(parts, "-")
	if base == "" {
		base = "event"
	}
	return base + ".ics"
}

// calendarICSHandler serves GET /calendar/event.ics?title=&label=&date= — a
// stateless, session-gated download of a single all-day key date as a valid
// .ics. Guarded exactly like artifactBlobHandler (method, origin, signed-in
// user). Inputs are length-capped; an unparseable date answers 400 so the
// frontend can show a "try a format like May 24" toast.
func calendarICSHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !websocketOriginAllowed(r) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	if userFromRequest(r) == nil {
		writeAuthError(w, http.StatusUnauthorized, "not signed in")
		return
	}

	query := r.URL.Query()
	title := trimForStorage(query.Get("title"), 200)
	label := trimForStorage(query.Get("label"), 200)
	rawDate := trimForStorage(query.Get("date"), 100)

	parsedDate, ok := parseKeyDateString(rawDate, time.Now())
	if !ok {
		writeAuthError(w, http.StatusBadRequest, "couldn't read that date")
		return
	}

	body := buildICSCalendar([]icsEvent{{Title: title, Label: label, Date: parsedDate}}, time.Now())
	filename := blobDownloadFilename(calendarICSFilename(title, label), "event.ics")

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if _, err := w.Write(body); err != nil {
		log.Errorf("Failed to serve calendar event: %v", err)
	}
}
