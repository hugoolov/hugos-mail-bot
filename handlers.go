package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// HandleGetEmails returns the email list with optional query-param filters:
//
//	?category=urgent
//	?min_importance=4
//	?action_needed=true
func HandleGetEmails(w http.ResponseWriter, r *http.Request) {
	emails := LoadEmails()

	if cat := r.URL.Query().Get("category"); cat != "" {
		var filtered []Email
		for _, e := range emails {
			if e.Category == cat {
				filtered = append(filtered, e)
			}
		}
		emails = filtered
	}

	if minStr := r.URL.Query().Get("min_importance"); minStr != "" {
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

	if r.URL.Query().Get("action_needed") == "true" {
		var filtered []Email
		for _, e := range emails {
			if e.ActionNeeded {
				filtered = append(filtered, e)
			}
		}
		emails = filtered
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(emails)
}

// HandleGetStats returns aggregate counts for the dashboard header cards.
func HandleGetStats(w http.ResponseWriter, r *http.Request) {
	emails := LoadEmails()

	stats := Stats{
		Total:      len(emails),
		Categories: make(map[string]int),
	}
	for _, e := range emails {
		stats.Categories[e.Category]++
		if e.Importance >= 4 {
			stats.Important++
		}
		if e.ActionNeeded {
			stats.ActionNeeded++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
