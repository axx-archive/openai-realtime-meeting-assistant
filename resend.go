package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const resendEndpoint = "https://api.resend.com/emails"

type sentAccountEmail struct {
	To      string
	Subject string
	HTML    string
}

func resendFromAddress() string {
	if from := strings.TrimSpace(os.Getenv("RESEND_FROM")); from != "" {
		return from
	}
	return "Bonfire <no-reply@thebonfire.xyz>"
}

// sendAccountEmail delivers transactional account email through Resend. With
// no RESEND_API_KEY configured (local/dev), the message body is logged
// instead so reset links remain reachable from the server logs. Declared as a
// var so tests can stub delivery.
var sendAccountEmail = func(to, subject, html string) error {
	apiKey := strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	if apiKey == "" {
		log.Infof("RESEND_API_KEY not set; account email for %s (%s) not sent. Body: %s", to, subject, html)
		return nil
	}

	payload, err := json.Marshal(map[string]any{
		"from":    resendFromAddress(),
		"to":      []string{to},
		"subject": subject,
		"html":    html,
	})
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPost, resendEndpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("resend rejected the email: status %d", response.StatusCode)
	}
	return nil
}

func passwordResetEmailHTML(name, resetURL string) string {
	return fmt.Sprintf(`<div style="font-family:sans-serif;max-width:480px;margin:0 auto">
<h2>BonfireOS password reset</h2>
<p>Hi %s — someone (hopefully you) asked to reset your BonfireOS password.</p>
<p><a href="%s" style="display:inline-block;padding:10px 18px;background:#1a1a1a;color:#fff;border-radius:8px;text-decoration:none">Reset your password</a></p>
<p>This link works once and expires in 30 minutes. If you didn't ask for it, you can ignore this email — your password is unchanged.</p>
</div>`, name, resetURL)
}
