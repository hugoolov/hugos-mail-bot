# 📬 Build an AI Email Bot in Go — Step-by-Step Guide

A complete walkthrough for building an IMAP email bot that uses Claude to
classify, sort, and surface important emails in a web dashboard.

---

## Architecture overview

```
┌──────────────┐      ┌──────────────┐      ┌────────────┐
│  IMAP Server │─────▶│  Go Backend  │─────▶│  JSON file  │
│ (your inbox) │      │  fetcher +   │      │  (storage)  │
└──────────────┘      │  classifier  │      └─────┬──────┘
                      └──────────────┘            │
                                                  ▼
                      ┌──────────────┐      ┌────────────┐
                      │   Browser    │◀─────│ Go HTTP    │
                      │  Dashboard   │      │ server     │
                      └──────────────┘      └────────────┘
```

The bot runs on a timer. Every N hours it:

1. Connects to your mailbox over IMAP/SSL
2. Fetches the last few days of email
3. Sends them to Claude for classification
4. Saves the results to a JSON file
5. Serves a dashboard so you can see everything


---

## Step 1 — Create the project

```bash
mkdir email-bot && cd email-bot
go mod init email-bot
```

Create this folder structure:

```
email-bot/
├── go.mod
├── go.sum
├── .env                  # your credentials (never commit this)
├── main.go               # entry point: scheduler + HTTP server
├── imap.go               # IMAP connection and email fetching
├── classifier.go         # Claude API integration
├── storage.go            # read/write the JSON data file
├── handlers.go           # HTTP API handlers for the dashboard
├── models.go             # shared data types
├── public/
│   └── index.html        # dashboard frontend
└── data/                 # auto-created at runtime
    └── emails.json       # classified email data
```

Everything lives in `package main` — no sub-packages needed for this size of project.


---

## Step 2 — Install dependencies

You need two external packages:

```bash
go get github.com/emersion/go-imap/v2
go get github.com/emersion/go-message
go get github.com/joho/godotenv
```

`go-imap/v2` is the modern, well-maintained IMAP client for Go.
`go-message` handles MIME parsing (multipart emails, encoded headers, etc.).
`godotenv` loads your `.env` file so you don't hardcode secrets.

That's it for external deps. You'll call the Claude API with `net/http` directly —
no SDK needed, it's just a POST request with JSON.


---

## Step 3 — Define your data types

Create **models.go** — this defines the shape of an email at every stage
of the pipeline (fetched, classified, stored).

```go
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
	ActionNeeded      bool      `json:"action_needed"`
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
	ActionNeeded      bool   `json:"action_needed"`
	ActionDescription string `json:"action_description"`
	Deadline          string `json:"deadline"`
}

// Stats provides aggregate numbers for the dashboard header.
type Stats struct {
	Total        int            `json:"total"`
	Important    int            `json:"important"`
	ActionNeeded int            `json:"action_needed"`
	Categories   map[string]int `json:"categories"`
}
```


---

## Step 4 — IMAP fetcher

Create **imap.go** — this connects to your mail server, searches for recent
messages, and parses each one into an `Email` struct.

```go
package main

import (
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"
)

// FetchEmails connects to the IMAP server, searches for emails
// from the last `daysBack` days, and returns up to `limit` parsed messages.
func FetchEmails(host, user, pass, folder string, port int, daysBack, limit int) ([]Email, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("📬 Connecting to %s...", addr)

	c, err := imapclient.DialTLS(addr, nil)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer c.Close()

	if err := c.Login(user, pass).Wait(); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	defer c.Logout()

	if _, err := c.Select(folder, nil).Wait(); err != nil {
		return nil, fmt.Errorf("select %s: %w", folder, err)
	}

	// Search for messages since N days ago
	since := time.Now().AddDate(0, 0, -daysBack)
	criteria := &imap.SearchCriteria{
		Since: since,
	}
	searchResult, err := c.Search(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	seqNums := searchResult.AllSeqNums()
	if len(seqNums) == 0 {
		log.Println("   No emails found in date range")
		return nil, nil
	}

	// Take only the most recent `limit` messages
	if len(seqNums) > limit {
		seqNums = seqNums[len(seqNums)-limit:]
	}
	log.Printf("   Found %d emails, fetching...", len(seqNums))

	seqSet := imap.SeqSetNum(seqNums...)
	fetchOpts := &imap.FetchOptions{
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}
	fetchCmd := c.Fetch(seqSet, fetchOpts)

	var emails []Email
	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}
		parsed, err := parseMessage(msg)
		if err != nil {
			log.Printf("   ⚠️  skip message: %v", err)
			continue
		}
		emails = append(emails, parsed)
	}

	if err := fetchCmd.Close(); err != nil {
		return emails, fmt.Errorf("fetch close: %w", err)
	}

	log.Printf("   ✅ Parsed %d emails", len(emails))
	return emails, nil
}

// parseMessage extracts the subject, sender, date, and body
// from a single IMAP message.
func parseMessage(msg *imapclient.FetchMessageData) (Email, error) {
	var e Email

	env := msg.Envelope
	if env != nil {
		e.Subject = env.Subject
		e.Date = env.Date
		if len(env.From) > 0 {
			from := env.From[0]
			if from.Name != "" {
				e.Sender = fmt.Sprintf("%s <%s@%s>", from.Name, from.Mailbox, from.Host)
			} else {
				e.Sender = fmt.Sprintf("%s@%s", from.Mailbox, from.Host)
			}
		}
	}

	e.UID = fmt.Sprintf("%d-%s", msg.SeqNum, e.Date.Format("20060102"))

	// Find the body section in the response
	for {
		item := msg.Next()
		if item == nil {
			break
		}
		section, ok := item.(imapclient.FetchItemDataBodySection)
		if !ok {
			continue
		}
		body, err := extractBody(section.Literal)
		if err != nil {
			log.Printf("   ⚠️  body extract: %v", err)
		}
		e.BodySnippet = truncate(body, 500)
		break
	}

	return e, nil
}

// extractBody reads the MIME structure and pulls out the plain text body.
// Falls back to the HTML part with tags stripped if no plain text exists.
func extractBody(r io.Reader) (string, error) {
	entity, err := message.Read(r)
	if err != nil {
		return "", err
	}

	// If it's not multipart, read the single part directly
	if mr := entity.MultipartReader(); mr != nil {
		var textBody, htmlBody string
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}

			ct, _, _ := part.Header.ContentType()
			raw, _ := io.ReadAll(part.Body)
			switch ct {
			case "text/plain":
				if textBody == "" {
					textBody = string(raw)
				}
			case "text/html":
				if htmlBody == "" {
					htmlBody = stripHTMLTags(string(raw))
				}
			}
		}
		if textBody != "" {
			return textBody, nil
		}
		return htmlBody, nil
	}

	// Single-part message
	raw, err := io.ReadAll(entity.Body)
	if err != nil {
		return "", err
	}

	h := mail.Header{Header: entity.Header}
	ct, _, _ := h.ContentType()
	if ct == "text/html" {
		return stripHTMLTags(string(raw)), nil
	}
	return string(raw), nil
}

func stripHTMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			result.WriteRune(' ')
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(result.String()), " ")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
```

**What's happening here:**

`FetchEmails` is the main entry point — it opens a TLS connection, logs in,
selects the folder (usually INBOX), and runs a date-based search. The search
returns sequence numbers which we then fetch with envelope + body data.

`parseMessage` extracts the structured fields (subject, sender, date) from the
IMAP envelope, then calls `extractBody` which walks the MIME tree looking for
`text/plain` first, falling back to `text/html` with tags stripped out.

The UID is synthesized from the sequence number and date — this gives us a
stable-enough key for deduplication across runs.


---

## Step 5 — Claude classifier

Create **classifier.go** — this sends a batch of emails to the Claude API
and gets back structured classifications.

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
)

var categories = []string{
	"urgent", "action", "finance", "scheduling",
	"newsletter", "social", "marketing", "other",
}

// claudeRequest / claudeResponse model the Anthropic Messages API.
type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// ClassifyEmails sends all emails to Claude in a single request
// and returns the updated slice with classification fields filled in.
func ClassifyEmails(emails []Email, apiKey string) []Email {
	if len(emails) == 0 {
		return emails
	}

	log.Printf("🤖 Classifying %d emails with Claude...", len(emails))

	prompt := buildPrompt(emails)
	body := claudeRequest{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 4096,
		Messages: []claudeMessage{
			{Role: "user", Content: prompt},
		},
	}

	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("❌ Claude API request failed: %v", err)
		return emails
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("❌ Claude API error %d: %s", resp.StatusCode, string(respBody))
		return emails
	}

	var result claudeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("❌ Failed to decode Claude response: %v", err)
		return emails
	}

	if len(result.Content) == 0 {
		log.Println("❌ Empty response from Claude")
		return emails
	}

	rawText := result.Content[0].Text

	// Strip markdown fences if present
	re := regexp.MustCompile("(?s)^```(?:json)?\\s*(.*)\\s*```$")
	if match := re.FindStringSubmatch(strings.TrimSpace(rawText)); len(match) > 1 {
		rawText = match[1]
	}

	var classifications []Classification
	if err := json.Unmarshal([]byte(rawText), &classifications); err != nil {
		log.Printf("❌ Failed to parse classifications: %v", err)
		log.Printf("   Raw response: %.500s", rawText)
		return emails
	}

	// Merge classifications back into the email structs
	for _, c := range classifications {
		if c.Index < 0 || c.Index >= len(emails) {
			continue
		}
		emails[c.Index].Category = c.Category
		emails[c.Index].Importance = c.Importance
		emails[c.Index].Summary = c.Summary
		emails[c.Index].ActionNeeded = c.ActionNeeded
		emails[c.Index].ActionDescription = c.ActionDescription
		emails[c.Index].Deadline = c.Deadline
	}

	important := 0
	for _, e := range emails {
		if e.Importance >= 4 {
			important++
		}
	}
	log.Printf("   ✅ Classified. %d marked as important.", important)

	return emails
}

func buildPrompt(emails []Email) string {
	var sb strings.Builder
	sb.WriteString("You are an email triage assistant. Classify each email below.\n\n")
	sb.WriteString("For EACH email, return a JSON object with:\n")
	sb.WriteString(`- "index": the email number` + "\n")
	sb.WriteString(fmt.Sprintf(`- "category": one of %s`+"\n", mustJSON(categories)))
	sb.WriteString(`- "importance": integer 1 (low) to 5 (critical)` + "\n")
	sb.WriteString(`- "summary": one-sentence summary` + "\n")
	sb.WriteString(`- "action_needed": true/false` + "\n")
	sb.WriteString(`- "action_description": what to do (empty string if no action)` + "\n")
	sb.WriteString(`- "deadline": extracted deadline or null` + "\n\n")
	sb.WriteString("Return ONLY a JSON array. No markdown fences, no extra text.\n\n")
	sb.WriteString("EMAILS:\n")

	for i, e := range emails {
		fmt.Fprintf(&sb, "--- EMAIL %d ---\nFrom: %s\nSubject: %s\nDate: %s\nBody: %s\n\n",
			i, e.Sender, e.Subject, e.Date.Format(time.RFC3339), e.BodySnippet)
	}
	return sb.String()
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
```

**What's happening here:**

`ClassifyEmails` builds a single prompt containing all the emails, sends it to
the Claude Messages API as a raw HTTP POST (no SDK needed), and parses the
JSON array response. Each classification is matched back to the original email
by its index number.

The `claudeRequest`/`claudeResponse` structs only model the fields we actually
use — you don't need to map the full API schema.

The regex at the end strips markdown code fences in case the model wraps its
JSON response in triple backticks (it sometimes does despite being told not to).

Note the `time` import is needed for `buildPrompt` — add it to your import block.


---

## Step 6 — Storage layer

Create **storage.go** — reads and writes the classified emails JSON file,
with deduplication so re-runs don't create duplicates.

```go
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
		log.Printf("⚠️  corrupt data file: %v", err)
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
	for _, e := range existing {
		seen[e.UID] = true
	}

	for _, e := range newEmails {
		if !seen[e.UID] {
			existing = append(existing, e)
			seen[e.UID] = true
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
```

**What's happening here:**

`LoadEmails` reads the JSON file from disk and returns the slice. If the file
doesn't exist yet (first run), it returns nil.

`SaveEmails` loads existing data, builds a set of seen UIDs, appends only
new emails that aren't already saved, sorts everything newest-first, and
writes it back. This means you can safely re-run the scheduler without
getting duplicate entries.


---

## Step 7 — HTTP handlers for the dashboard

Create **handlers.go** — two JSON endpoints that the frontend calls.

```go
package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// HandleGetEmails returns the email list with optional query-param filters:
//   ?category=urgent
//   ?min_importance=4
//   ?action_needed=true
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
```

**What's happening here:**

These are plain `http.HandlerFunc` functions — no framework needed. The email
endpoint supports three optional filters via query parameters. Both handlers
read directly from the JSON file each time, which is fine for a personal tool
(no need for an in-memory cache or database).


---

## Step 8 — Main entry point (scheduler + server)

Create **main.go** — ties everything together. Runs the fetch/classify pipeline
on a timer and serves the dashboard simultaneously.

```go
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
```

**What's happening here:**

`main` loads the `.env` file, reads all config values with sensible defaults,
and starts two things in parallel:

- A **goroutine** that runs the pipeline immediately on startup and then
  repeats on a `time.Ticker` interval. This is Go's idiomatic way to do
  scheduled work — no cron or external scheduler needed.

- The **HTTP server** on the main goroutine, serving the static dashboard
  files from `public/` and the two JSON API endpoints.

`runPipeline` is the glue function that calls fetch → classify → save in
sequence and logs the results.


---

## Step 9 — Create the .env file

```bash
cp this template to .env and fill in your values:
```

```
# IMAP connection
IMAP_HOST=imap.example.com
IMAP_PORT=993
IMAP_USER=you@example.com
IMAP_PASS=your-password-or-app-password
IMAP_FOLDER=INBOX

# Claude API
ANTHROPIC_API_KEY=sk-ant-...

# Scheduler
INTERVAL_HOURS=4
DAYS_BACK=3
EMAIL_LIMIT=50

# Dashboard
DASH_PORT=5000
```

> **Tip:** Most providers require an "app password" instead of your regular
> password. Check your provider's security settings. For Gmail you'll need
> to enable 2FA first, then generate an app password.


---

## Step 10 — Add the dashboard frontend

Create **public/index.html** — this is the same dashboard that calls your
Go API endpoints. I'll provide this as a separate file when we build it.


---

## Step 11 — Build and run

```bash
# Build
go build -o email-bot .

# Run (fetches immediately, then every INTERVAL_HOURS)
./email-bot

# Open in browser
open http://localhost:5000
```

You should see:
1. Console output showing IMAP connection and email fetching
2. Claude classification results with importance scores
3. The dashboard at localhost:5000 showing your sorted inbox


---

## Common IMAP hosts

| Provider         | Host                      | Port | Notes                          |
|------------------|---------------------------|------|--------------------------------|
| Gmail            | imap.gmail.com            | 993  | Needs app password             |
| Outlook/Hotmail  | outlook.office365.com     | 993  | May need app password          |
| Fastmail         | imap.fastmail.com         | 993  | App password in settings       |
| Yahoo            | imap.mail.yahoo.com       | 993  | App password required          |
| ProtonMail       | 127.0.0.1                 | 1143 | Needs ProtonMail Bridge        |
| iCloud           | imap.mail.me.com          | 993  | App-specific password          |


---

## What's next?

Once the basic bot is running, here are good extensions:

- **Auto-labeling**: Use IMAP STORE to add flags/labels back to your inbox
- **Email notifications**: Send yourself a Slack or Telegram message for importance >= 4
- **Better storage**: Swap the JSON file for SQLite if you want search and history
- **Filtering rules**: Let users define custom rules ("emails from boss = always urgent")
- **OAuth2**: Replace app passwords with proper OAuth for Gmail/Outlook
