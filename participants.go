package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"os"
	"strconv"
	"strings"
)

const (
	defaultMeetingRoomPassword = "B0NFIRE!"
	defaultMeetingRoomCapacity = 10
)

var meetingParticipantNames = []string{
	"Erick",
	"Tim",
	"Tyler",
	"Jake",
	"Tom",
	"Caitlyn",
	"Joel",
	"AJ",
	"Guest 1",
	"Guest 2",
}

func canonicalParticipantName(name string) string {
	normalizedName := strings.TrimSpace(name)
	for _, candidate := range meetingParticipantNames {
		if strings.EqualFold(normalizedName, candidate) {
			return candidate
		}
	}

	return ""
}

func validMeetingPassword(password string) bool {
	providedPassword := strings.TrimSpace(password)
	configuredPassword := configuredMeetingRoomPassword()
	providedHash := sha256.Sum256([]byte(providedPassword))
	configuredHash := sha256.Sum256([]byte(configuredPassword))

	return subtle.ConstantTimeCompare(providedHash[:], configuredHash[:]) == 1
}

func configuredMeetingRoomPassword() string {
	if password := strings.TrimSpace(os.Getenv("MEETING_ROOM_PASSWORD")); password != "" {
		return password
	}

	return defaultMeetingRoomPassword
}

func configuredMeetingRoomCapacity() int {
	rawCapacity := strings.TrimSpace(os.Getenv("MEETING_ROOM_MAX_PARTICIPANTS"))
	if rawCapacity == "" {
		return defaultMeetingRoomCapacity
	}

	capacity, err := strconv.Atoi(rawCapacity)
	if err != nil || capacity < 1 {
		return defaultMeetingRoomCapacity
	}

	return capacity
}

func participantEmail(name string) string {
	name = canonicalParticipantName(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(name), "guest ") {
		return ""
	}
	if strings.EqualFold(name, "Erick") {
		return "e@shareability.com"
	}

	return strings.ToLower(name) + "@shareability.com"
}

func participantEmails(names []string) []string {
	emails := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		email := participantEmail(name)
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		emails = append(emails, email)
	}

	return emails
}
