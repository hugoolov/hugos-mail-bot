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

func FetchEmails(host, user, pass, folder string, port int, daysBack, limit int) ([]Email, error) {
	address := fmt.Sprintf("%s:%d", host, port)
	log.Printf("Connecting to %s\n", address)

	client, err := imapclient.DialTLS(address, nil)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer client.Close()

	if err := client.Login(user, pass).Wait(); err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	defer client.Logout()

	if _, err := client.Select(folder, nil).Wait(); err != nil {
		return nil, fmt.Errorf("select %s: %w", folder, err)
	}

	since := time.Now().AddDate(0, 0, -daysBack)
	criteria := &imap.SearchCriteria{Since: since}

	searchResuls, err := client.Search(criteria, nil).Wait()
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	seqNums := searchResuls.AllSeqNums()
	if len(seqNums) == 0 {
		log.Println("   No emails found in range")
		return nil, nil
	}
	if len(seqNums) > limit {
		seqNums = seqNums[len(seqNums)-limit:]
	}
	log.Printf("   Found %d emails\n", len(seqNums))

	seqSet := imap.SeqSetNum(seqNums...)
	fetchOpts := &imap.FetchOptions{
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}
	fetchCmd := client.Fetch(seqSet, fetchOpts)

	var emails []Email
	for {
		message := fetchCmd.Next()
		if message == nil {
			break
		}
		parsed, err := parseMessage(message)
		if err != nil {
			log.Printf("   Error parsing email: %s\n, skipping", err)
			continue
		}
		emails = append(emails, parsed)
	}
	if err := fetchCmd.Close(); err != nil {
		return emails, fmt.Errorf("Fetch close: %w", err)
	}

	log.Printf("Parsed %d emails\n", len(emails))
	return emails, nil
}

func parseMessage(message *imapclient.FetchMessageData) (Email, error) {
	var email Email

	env := message.Envelope
	if env != nil {
		email.Subject = env.Subject
		email.Date = env.Date
		if len(env.From) > 0 {
			from := env.From[0]
			if from.Name != "" {
				email.Sender = fmt.Sprintf("%s <%s@%s>", from.Name, from.Mailbox, from.Host)
			} else {
				email.Sender = fmt.Sprintf("%s@%s", from.Mailbox, from.Host)
			}
		}
	}

	email.UID = fmt.Sprintf("%d-%s", message.SeqNum, email.Date.Format("20060102"))

	for {
		item := message.Next()
		if item == nil {
			break
		}
		section, ok := item.(imapclient.FetchItemDataBodySection)
		if !ok {
			continue
		}
		body, err := extractBody(section.Literal)
		if err != nil {
			log.Printf("   Error extracting body: %v", err)
		}
		email.BodySnipet = truncate(body, 500)
		break
	}
	return email, nil
}

func extractBody(read io.reader) (string, error) {
	entity, err := message.Read(read)
	if multipartReader := entity.MultipartReader(); multipartReader != nil {
		var textBody, htmlBody string
		for {
			part, err := multipartReader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			contentType, _, _ := part.Header.ContentType()
			raw, _ := io.ReadAll(part.Body)
			switch contentType {
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

	raw, err := io.ReadAll(entity.Body)
	if err != nil {
		return "", err
	}

	header := mail.Header{Header: entity.Header}
	contentType, _, _ := header.ContentType()
	if contentType == "text/html" {
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
