package main

import "time"

// Email holds everything about a single message:
// the raw IMAP data plus the AI classification results.
type Email struct {
	UID               string    `json:"uid"`
	Subject           string    `json:"subject"`
	Sender            string    `json:"sender"`
	Date              time.Time `json:"date"`
	BodySnippet       string    `json:"body_snippet"`
	Category          string    `json:"category"`
	Importance        int       `json:"importance"`
	Summary           string    `json:"summary"`
	ActionRequired    bool      `json:"action_required"`
	ActionDescription string    `json:"action_description"`
	Deadline          string    `json:"deadline,omitempty"`
}

// Classification is what Claude returns for each email.
// It maps to one entry in the JSON array response.
type Classification struct {
	Index             int    `json:"index"`
	Category          string `json:"category"`
	Importance        int    `json:"importance"`
	Summary           string `json:"summary"`
	ActionRequired    bool   `json:"action_required"`
	ActionDescription string `json:"action_description"`
	Deadline          string `json:"deadline"`
}

// Stats provides aggregate numbers for the dashboard header.
type Stats struct {
	Total          int            `json:"total"`
	Important      int            `json:"important"`
	ActionRequired int            `json:"action_required"`
	Categories     map[string]int `json:"categories"`
}
