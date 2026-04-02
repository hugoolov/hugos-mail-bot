package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	godotenv.Load()

	imapHost := os.Getenv("IMAP_HOST")
	imapUser := os.Getenv("IMAP_USER")
	imapPass := os.Getenv("IMAP_PASS")
	imapFolder := envOr("IMAP_FOLDER", "INBOX")
	imapPort, _ := strconv.Atoi(envOr("IMAP_PORT", "993"))
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	intervalHours, _ := strconv.ParseFloat(envOr("INTERVAL_HOURS", "4"), 64)
	daysBack, _ := strconv.Atoi(envOr("DAYS_BACK", "3"))
	emailLimit, _ := strconv.Atoi(envOr("EMAIL_LIMIT", "50"))
	dashPort := envOr("DASH_PORT", "5000")

	if imapHost == "" || imapUser == "" || imapPass == "" {
		log.Fatal("❌ Set IMAP_HOST, IMAP_USER, and IMAP_PASS in .env")
	}
	if apiKey == "" {
		log.Fatal("❌ Set ANTHROPIC_API_KEY in .env")
	}

	// Run the pipeline once immediately on startup, then on a timer
	go func() {
		runPipeline(imapHost, imapUser, imapPass, imapFolder, imapPort, daysBack, emailLimit, apiKey)

		ticker := time.NewTicker(time.Duration(intervalHours * float64(time.Hour)))
		defer ticker.Stop()
		for range ticker.C {
			runPipeline(imapHost, imapUser, imapPass, imapFolder, imapPort, daysBack, emailLimit, apiKey)
		}
	}()

	// Serve the dashboard
	http.Handle("/", http.FileServer(http.Dir("public")))
	http.HandleFunc("/api/emails", HandleGetEmails)
	http.HandleFunc("/api/stats", HandleGetStats)

	log.Printf("🌐 Dashboard running at http://localhost:%s", dashPort)
	log.Printf("⏰ Fetching every %.1f hours, looking back %d days", intervalHours, daysBack)
	log.Fatal(http.ListenAndServe(":"+dashPort, nil))
}

// runPipeline executes one full fetch → classify → save cycle.
func runPipeline(host, user, pass, folder string, port, daysBack, limit int, apiKey string) {
	now := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("\n%s\n⏰ Run started at %s\n%s\n",
		"============================================================",
		now,
		"============================================================")

	emails, err := FetchEmails(host, user, pass, folder, port, daysBack, limit)
	if err != nil {
		log.Printf("❌ Fetch error: %v", err)
		return
	}
	if len(emails) == 0 {
		log.Println("📭 No emails to classify")
		return
	}

	classified := ClassifyEmails(emails, apiKey)
	if err := SaveEmails(classified); err != nil {
		log.Printf("❌ Save error: %v", err)
		return
	}

	log.Printf("✅ Done. %d emails processed.", len(classified))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
