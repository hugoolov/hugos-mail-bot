package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// HandleGetEmails returns the email list with optional query-param filters

func HandleGetEmails(responseWriter http.ResponseWriter, request *http.Request) {
	emails := LoadEmails()

	if cat := request.URL.Query().Get("category"); cat != "" {
		var filtered []Email
		for _, email := range emails {
			if email.Category == cat {
				filtered = append(filtered, email)
			}
		}
		emails = filtered
	}

	if minStr := request.URL.Query().Get("min_importance"); minStr != "" {
		if minImp, err := strconv.Atoi(minStr); err == nil {
			var filtered []Email
			for _, e := range emails {
				if e.Importance >= minImp {
					filtered = append(filtered, e)
				}
			}
			emails = filtered
		}
	}

	if request.URL.Query().Get("action_needed") == "true" {
		var filtered []Email
		for _, email := range emails {
			if email.ActionRequired {
				filtered = append(filtered, email)
			}
		}
		emails = filtered
	}

	responseWriter.Header().Set("Content-Type", "application/json")
	json.NewEncoder(responseWriter).Encode(emails)
}

// HandleGetStats returns aggregate counts for the dashboard header cards.
func HandleGetStats(w http.ResponseWriter, r *http.Request) {
	emails := LoadEmails()

	stats := Stats{
		Total:      len(emails),
		Categories: make(map[string]int),
	}
	for _, email := range emails {
		stats.Categories[email.Category]++
		if email.Importance >= 4 {
			stats.Important++
		}
		if email.ActionRequired {
			stats.ActionRequired++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
