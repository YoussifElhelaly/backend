package mailer

import (
	"fmt"
	"log"
	"net/mail"
	"net/smtp"
	"os"
)

// Send sends an HTML email. If SMTP_HOST is not set, it logs the email to
// stdout (useful in local dev without an SMTP server).
func Send(to, subject, body string) error {
	host := os.Getenv("SMTP_HOST")
	port := os.Getenv("SMTP_PORT")
	user := os.Getenv("SMTP_USER")
	pass := os.Getenv("SMTP_PASS")
	fromFull := os.Getenv("SMTP_FROM") // e.g. "Whatify <noreply@example.com>"

	if host == "" {
		log.Printf("[MAILER] To: %s | Subject: %s\n%s\n", to, subject, body)
		return nil
	}

	if port == "" {
		port = "587"
	}
	if fromFull == "" {
		fromFull = user
	}

	// smtp.SendMail requires a bare email address for the MAIL FROM envelope.
	// net/mail.ParseAddress handles both "addr" and "Name <addr>" formats.
	fromEnvelope := fromFull
	if addr, err := mail.ParseAddress(fromFull); err == nil {
		fromEnvelope = addr.Address
	}

	raw := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s",
		fromFull, to, subject, body,
	)

	auth := smtp.PlainAuth("", user, pass, host)
	if err := smtp.SendMail(host+":"+port, auth, fromEnvelope, []string{to}, []byte(raw)); err != nil {
		log.Printf("[MAILER] failed to send to %s: %v", to, err)
		return err
	}
	return nil
}
