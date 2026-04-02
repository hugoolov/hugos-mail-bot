package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
)

var dataDir = "data"
var dataFile = filepath.Join(dataDir, "emails.json")

// LoadEmails reads previously saved emails from disk.
func LoadEmails() []Email {
	raw, err := os.ReadFile(dataFile)
	if err != nil {
		return nil
	}
	var emails []Email
	if err := json.Unmarshal(raw, &emails); err != nil {
		log.Printf("corrupt data file: %v", err)
		return nil
	}
	return emails
}

// SaveEmails merges new emails with existing ones (deduped by UID),
// sorts by date descending, and writes to disk.
func SaveEmails(newEmails []Email) error {
	os.MkdirAll(dataDir, 0755)

	existing := LoadEmails()

	seen := make(map[string]bool)
	for _, email := range existing {
		seen[email.UID] = true
	}

	for _, email := range newEmails {
		if !seen[email.UID] {
			existing = append(existing, email)
			seen[email.UID] = true
		}
	}

	// Newest first
	sort.Slice(existing, func(i, j int) bool {
		return existing[i].Date.After(existing[j].Date)
	})

	raw, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dataFile, raw, 0644)
}
