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
	"time"
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
