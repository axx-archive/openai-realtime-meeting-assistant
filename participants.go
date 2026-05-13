package main

import "strings"

const meetingRoomPassword = "B0NFIRE!"

var meetingParticipantNames = []string{
	"Erick",
	"Tim",
	"Tyler",
	"Jake",
	"Tom",
	"Caitlyn",
	"Joel",
	"AJ",
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
	return strings.TrimSpace(password) == meetingRoomPassword
}
