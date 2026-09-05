package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// The second way an event can reach an operator.
//
// Telegram is the one people reach for, and it is the one that stops working
// when a bot token is revoked, when Telegram is blocked on the server's
// network, or when the operator is somewhere Telegram is not. Mail is slower
// and duller and keeps working, which is what an alert channel is for. The two
// are independent: one failing does not stop the other, and an event goes to
// whichever channels asked for it.

// Encryption is how the session with the mail server is protected.
type Encryption string

const (
	// EncryptionStartTLS connects in the clear on the submission port and
	// upgrades. This is what port 587 expects and what most providers want.
	EncryptionStartTLS Encryption = "starttls"
	// EncryptionTLS connects wrapped from the first byte, which is port 465.
	EncryptionTLS Encryption = "tls"
	// EncryptionNone is for a mail server on this same machine. Offered
	// because it is sometimes right, and named plainly so nobody picks it by
	// accident.
	EncryptionNone Encryption = "none"
)

// mailTimeout bounds a whole delivery: connect, greet, authenticate, send.
const mailTimeout = 30 * time.Second

// MailConfig is what the email settings page stores.
type MailConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	FromName string `json:"fromName"`
	// To may hold several addresses separated by commas. An operator with a
	// colleague should not need a mailing list to add them.
	To         string          `json:"to"`
	Encryption Encryption      `json:"encryption"`
	Kinds      map[string]bool `json:"kinds"`
}

// Wants reports whether this kind should be mailed.
//
// Deliberately its own list rather than sharing Telegram's: the whole point of
// a second channel is that it can be set to carry less. Telegram every time a
// customer runs out, mail only when the panel itself is in trouble.
func (c MailConfig) Wants(k Kind) bool {
	if !c.Enabled || c.Host == "" || c.From == "" || c.To == "" {
		return false
	}
	if len(c.Kinds) == 0 {
		return true // nothing chosen yet means everything, not nothing
	}
	return c.Kinds[string(k)]
}

// recipients splits the To field into addresses the server will accept.
func (c MailConfig) recipients() []string {
	var out []string
	for _, part := range strings.FieldsFunc(c.To, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n'
	}) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// SetMail replaces the mail settings. Safe to call while running.
func (n *Notifier) SetMail(c MailConfig) {
	n.mu.Lock()
	n.mail = c
	n.mu.Unlock()
}

// Mail returns the current mail settings.
func (n *Notifier) Mail() MailConfig {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.mail
}

// TestMail sends one message and reports what happened, so an operator finds
// out their password is wrong on the settings page rather than the night the
// server has a problem.
func (n *Notifier) TestMail(ctx context.Context, c MailConfig) error {
	switch {
	case c.Host == "":
		return errors.New("notify: a mail server is required")
	case c.From == "":
		return errors.New("notify: a from address is required")
	case len(c.recipients()) == 0:
		return errors.New("notify: at least one recipient is required")
	}
	return n.sendMail(ctx, c, Event{
		Kind:  KindPanel,
		Title: "W-UI",
		Body:  "Email notifications are working.",
		At:    time.Now(),
	})
}

// sendMail delivers one event by SMTP.
func (n *Notifier) sendMail(ctx context.Context, c MailConfig, e Event) error {
	if c.Port == 0 {
		c.Port = 587
	}
	addr := net.JoinHostPort(c.Host, fmt.Sprint(c.Port))

	ctx, cancel := context.WithTimeout(ctx, mailTimeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("notify: connect to %s: %w", addr, err)
	}
	// Bounds every step after the connection too: a mail server that accepts a
	// connection and then stops talking is the common failure, and without
	// this the send would hang until the process ended.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if c.Encryption == EncryptionTLS {
		conn = tls.Client(conn, &tls.Config{ServerName: c.Host})
	}

	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("notify: greet %s: %w", c.Host, err)
	}
	defer client.Close()

	if c.Encryption == EncryptionStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("notify: %s does not offer STARTTLS; "+
				"choose another encryption setting or another port", c.Host)
		}
		if err := client.StartTLS(&tls.Config{ServerName: c.Host}); err != nil {
			return fmt.Errorf("notify: start TLS with %s: %w", c.Host, err)
		}
	}

	if c.Username != "" {
		// PLAIN over an encrypted session. Refused rather than sent in the
		// clear: a password on the wire to save an operator one setting is not
		// a trade this makes for them.
		if c.Encryption == EncryptionNone {
			return errors.New("notify: refusing to send a mail password over an " +
				"unencrypted connection; choose STARTTLS or TLS")
		}
		if err := client.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return fmt.Errorf("notify: sign in to %s: %w", c.Host, err)
		}
	}

	if err := client.Mail(c.From); err != nil {
		return fmt.Errorf("notify: sender %q refused: %w", c.From, err)
	}
	for _, to := range c.recipients() {
		if err := client.Rcpt(to); err != nil {
			return fmt.Errorf("notify: recipient %q refused: %w", to, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("notify: start message: %w", err)
	}
	if _, err := w.Write([]byte(mailMessage(c, e))); err != nil {
		w.Close()
		return fmt.Errorf("notify: write message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("notify: send message: %w", err)
	}
	return client.Quit()
}

// mailMessage renders the headers and body of one notification.
func mailMessage(c MailConfig, e Event) string {
	from := c.From
	if c.FromName != "" {
		from = fmt.Sprintf("%s <%s>", headerSafe(c.FromName), c.From)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(c.recipients(), ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", headerSafe(e.Title))
	fmt.Fprintf(&b, "Date: %s\r\n", e.At.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")

	if e.Body != "" {
		b.WriteString(e.Body)
		b.WriteString("\r\n\r\n")
	}
	fmt.Fprintf(&b, "%s\r\n", e.At.Format("2006-01-02 15:04:05 MST"))
	return b.String()
}

// headerSafe strips what would let a value break out of its header.
//
// A customer's name reaches a subject line, and a newline in it would end the
// header and let whatever followed be read as another one -- a Bcc, say. There
// is no legitimate newline in any of these values, so they are simply removed.
func headerSafe(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
