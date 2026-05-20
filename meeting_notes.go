package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"sort"
	"strings"
	"time"
)

type meetingNotes struct {
	Subject         string                 `json:"subject"`
	Text            string                 `json:"text"`
	Decisions       []string               `json:"decisions"`
	ProjectStatuses []meetingProjectStatus `json:"projectStatuses"`
	GeneratedAt     time.Time              `json:"generatedAt"`
}

type meetingProjectStatus struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Status   string          `json:"status"`
	Owner    string          `json:"owner"`
	Notes    string          `json:"notes"`
	Tags     []string        `json:"tags,omitempty"`
	DueDate  string          `json:"dueDate,omitempty"`
	KeyDates []kanbanKeyDate `json:"keyDates,omitempty"`
}

type meetingEmailStatus struct {
	Attempted  bool     `json:"attempted"`
	Sent       bool     `json:"sent"`
	Skipped    bool     `json:"skipped"`
	Error      string   `json:"error,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Recipients []string `json:"recipients,omitempty"`
}

type meetingNotesSMTPConfig struct {
	Host             string
	Port             string
	Username         string
	Password         string
	From             string
	DisableStartTLS  bool
	InsecureSkipTLS  bool
	AllowNoRecipient bool
}

const smtpConnectionTimeout = 10 * time.Second

var decisionKeywords = []string{
	"decided",
	"decision",
	"agreed",
	"approved",
	"we will",
	"we'll",
	"action item",
	"follow up",
	"next step",
	"resolved",
	"committed",
	"blocked on",
	"waiting on",
}

func buildMeetingNotes(archiveID string, archivedAt time.Time, archivedBy string, board kanbanBoardState, memory []meetingMemoryEntry, participants []string) meetingNotes {
	projectStatuses := make([]meetingProjectStatus, 0, len(board.Cards))
	for _, card := range board.Cards {
		projectStatuses = append(projectStatuses, meetingProjectStatus{
			ID:       card.ID,
			Title:    card.Title,
			Status:   string(card.Status),
			Owner:    card.Owner,
			Notes:    card.Notes,
			Tags:     append([]string(nil), card.Tags...),
			DueDate:  card.DueDate,
			KeyDates: cloneKanbanKeyDates(card.KeyDates),
		})
	}

	decisions := extractDecisionItems(memory, 10)
	subject := fmt.Sprintf("Meeting notes - %s", archivedAt.Format("Jan 2, 2006"))
	notes := meetingNotes{
		Subject:         subject,
		Decisions:       decisions,
		ProjectStatuses: projectStatuses,
		GeneratedAt:     time.Now().UTC(),
	}
	notes.Text = renderMeetingNotesText(archiveID, archivedAt, archivedBy, participants, notes)

	return notes
}

func extractDecisionItems(memory []meetingMemoryEntry, limit int) []string {
	if limit <= 0 {
		return nil
	}

	items := make([]meetingMemoryEntry, 0, len(memory))
	for _, entry := range memory {
		if entry.Kind != meetingMemoryKindTranscript {
			continue
		}
		if !memoryTextLooksLikeDecision(entry.Text) {
			continue
		}
		items = append(items, entry)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	if len(items) > limit {
		items = items[len(items)-limit:]
	}

	decisions := make([]string, 0, len(items))
	for _, item := range items {
		decisions = append(decisions, item.Text)
	}

	return decisions
}

func memoryTextLooksLikeDecision(text string) bool {
	normalized := strings.ToLower(normalizeMemoryText(text))
	if normalized == "" {
		return false
	}
	for _, keyword := range decisionKeywords {
		if strings.Contains(normalized, keyword) {
			return true
		}
	}

	return false
}

func renderMeetingNotesText(archiveID string, archivedAt time.Time, archivedBy string, participants []string, notes meetingNotes) string {
	var body strings.Builder
	body.WriteString("The Bonfire meeting notes\n")
	body.WriteString("=========================\n\n")
	body.WriteString(fmt.Sprintf("Archived: %s\n", archivedAt.Format(time.RFC1123)))
	if archivedBy != "" {
		body.WriteString(fmt.Sprintf("Archived by: %s\n", archivedBy))
	}
	if len(participants) > 0 {
		body.WriteString(fmt.Sprintf("Participants: %s\n", strings.Join(participants, ", ")))
	}
	if archiveID != "" {
		body.WriteString(fmt.Sprintf("Archive ID: %s\n", archiveID))
	}

	body.WriteString("\nWhat we decided today\n")
	body.WriteString("---------------------\n")
	if len(notes.Decisions) == 0 {
		body.WriteString("- No explicit decisions were captured in the transcript.\n")
	} else {
		for _, decision := range notes.Decisions {
			body.WriteString("- ")
			body.WriteString(decision)
			body.WriteByte('\n')
		}
	}

	body.WriteString("\nLatest status for each project\n")
	body.WriteString("------------------------------\n")
	if len(notes.ProjectStatuses) == 0 {
		body.WriteString("- No active project cards were on the board.\n")
	} else {
		for _, project := range notes.ProjectStatuses {
			owner := strings.TrimSpace(project.Owner)
			if owner == "" {
				owner = "Unassigned"
			}
			body.WriteString(fmt.Sprintf("- %s: %s. Owner: %s", project.Title, project.Status, owner))
			if len(project.Tags) > 0 {
				body.WriteString(fmt.Sprintf(". Tags: %s", strings.Join(project.Tags, ", ")))
			}
			if project.DueDate != "" {
				body.WriteString(fmt.Sprintf(". Due: %s", project.DueDate))
			}
			if len(project.KeyDates) > 0 {
				body.WriteString(fmt.Sprintf(". Key dates: %s", formatKanbanKeyDates(project.KeyDates)))
			}
			body.WriteByte('\n')
			if strings.TrimSpace(project.Notes) != "" {
				body.WriteString("  Notes: ")
				body.WriteString(project.Notes)
				body.WriteByte('\n')
			}
		}
	}

	return body.String()
}

func sendMeetingNotesEmail(recipients []string, notes meetingNotes) meetingEmailStatus {
	status := meetingEmailStatus{
		Recipients: append([]string(nil), recipients...),
	}
	if len(recipients) == 0 {
		status.Skipped = true
		status.Reason = "No meeting participants with email addresses."
		return status
	}

	config := meetingNotesSMTPConfigFromEnv()
	if !config.configured() {
		status.Skipped = true
		status.Reason = "SMTP is not configured."
		return status
	}

	status.Attempted = true
	if err := deliverSMTPMeetingNotes(config, recipients, notes); err != nil {
		status.Error = err.Error()
		return status
	}

	status.Sent = true
	return status
}

func meetingNotesSMTPConfigFromEnv() meetingNotesSMTPConfig {
	port := strings.TrimSpace(os.Getenv("MEETING_NOTES_SMTP_PORT"))
	if port == "" {
		port = "587"
	}
	from := strings.TrimSpace(os.Getenv("MEETING_NOTES_SMTP_FROM"))
	if from == "" {
		from = "meeting-notes@shareability.com"
	}

	return meetingNotesSMTPConfig{
		Host:            strings.TrimSpace(os.Getenv("MEETING_NOTES_SMTP_HOST")),
		Port:            port,
		Username:        strings.TrimSpace(os.Getenv("MEETING_NOTES_SMTP_USERNAME")),
		Password:        os.Getenv("MEETING_NOTES_SMTP_PASSWORD"),
		From:            from,
		DisableStartTLS: boolEnv("MEETING_NOTES_SMTP_DISABLE_STARTTLS"),
		InsecureSkipTLS: boolEnv("MEETING_NOTES_SMTP_INSECURE_SKIP_VERIFY"),
	}
}

func (config meetingNotesSMTPConfig) configured() bool {
	return strings.TrimSpace(config.Host) != ""
}

func deliverSMTPMeetingNotes(config meetingNotesSMTPConfig, recipients []string, notes meetingNotes) error {
	host := strings.TrimSpace(config.Host)
	if host == "" {
		return fmt.Errorf("SMTP host is required")
	}

	client, err := newSMTPClient(config)
	if err != nil {
		return err
	}
	defer client.Quit() //nolint:errcheck

	if config.Username != "" || config.Password != "" {
		if err := client.Auth(smtp.PlainAuth("", config.Username, config.Password, host)); err != nil {
			return fmt.Errorf("authenticate SMTP client: %w", err)
		}
	}

	from := smtpEnvelopeAddress(config.From)
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("set SMTP recipient %s: %w", recipient, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP data writer: %w", err)
	}
	if _, err := writer.Write(buildMeetingNotesEmailMessage(config.From, recipients, notes)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close SMTP data writer: %w", err)
	}

	return nil
}

func newSMTPClient(config meetingNotesSMTPConfig) (*smtp.Client, error) {
	host := strings.TrimSpace(config.Host)
	port := strings.TrimSpace(config.Port)
	address := net.JoinHostPort(host, port)
	dialer := &net.Dialer{
		Timeout: smtpConnectionTimeout,
	}
	tlsConfig := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: config.InsecureSkipTLS, //nolint:gosec
	}

	if port == "465" {
		connection, err := tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
		if err != nil {
			return nil, fmt.Errorf("connect to SMTP over TLS: %w", err)
		}
		client, err := smtp.NewClient(connection, host)
		if err != nil {
			_ = connection.Close()
			return nil, fmt.Errorf("create SMTP client: %w", err)
		}
		return client, nil
	}

	connection, err := dialer.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("connect to SMTP: %w", err)
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("create SMTP client: %w", err)
	}
	if !config.DisableStartTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				_ = client.Close()
				return nil, fmt.Errorf("start SMTP TLS: %w", err)
			}
		}
	}

	return client, nil
}

func buildMeetingNotesEmailMessage(from string, recipients []string, notes meetingNotes) []byte {
	var message bytes.Buffer
	headers := map[string]string{
		"From":                      sanitizeHeader(from),
		"To":                        sanitizeHeader(strings.Join(recipients, ", ")),
		"Subject":                   mime.QEncoding.Encode("utf-8", notes.Subject),
		"Date":                      time.Now().Format(time.RFC1123Z),
		"MIME-Version":              "1.0",
		"Content-Type":              "text/plain; charset=UTF-8",
		"Content-Transfer-Encoding": "8bit",
	}
	headerOrder := []string{"From", "To", "Subject", "Date", "MIME-Version", "Content-Type", "Content-Transfer-Encoding"}
	for _, key := range headerOrder {
		message.WriteString(key)
		message.WriteString(": ")
		message.WriteString(headers[key])
		message.WriteString("\r\n")
	}
	message.WriteString("\r\n")
	message.WriteString(notes.Text)

	return message.Bytes()
}

func smtpEnvelopeAddress(value string) string {
	parsed, err := mail.ParseAddress(value)
	if err == nil {
		return parsed.Address
	}

	return strings.TrimSpace(value)
}

func sanitizeHeader(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.TrimSpace(value))
}

func boolEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
