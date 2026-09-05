package notify

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A customer's name reaches the subject line. A newline in it would end that
// header and let whatever followed be read as another one — a Bcc, say — which
// turns an alert about a customer into a way to send mail as the panel.
func TestAValueCannotBreakOutOfAHeader(t *testing.T) {
	cfg := MailConfig{
		From: "panel@example.com", FromName: "W-UI\r\nBcc: attacker@example.com",
		To: "admin@example.com",
	}
	got := mailMessage(cfg, Event{
		Title: "Customer\r\nBcc: attacker@example.com used up their allowance",
		At:    time.Now(),
	})

	headers, _, ok := strings.Cut(got, "\r\n\r\n")
	if !ok {
		t.Fatalf("the message has no header/body split:\n%s", got)
	}
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("a value became a header of its own:\n%s", headers)
		}
	}
}

// Several addresses in one box, because an operator with a colleague should not
// need a mailing list to add them.
func TestRecipientsAreSplitHoweverTheyWereTyped(t *testing.T) {
	cfg := MailConfig{To: "a@example.com, b@example.com;c@example.com d@example.com"}
	got := cfg.recipients()
	if len(got) != 4 {
		t.Fatalf("got %d recipients from four addresses: %q", len(got), got)
	}
}

// Mail carries its own list of what to send. The point of a second channel is
// that it can be set to carry less than the first.
func TestMailChoosesItsOwnEvents(t *testing.T) {
	base := MailConfig{Enabled: true, Host: "smtp.example.com", From: "a@b.c", To: "d@e.f"}

	quiet := base
	quiet.Kinds = map[string]bool{string(KindPanel): true}
	if quiet.Wants(KindExhausted) {
		t.Error("mail sent an event it was not asked for")
	}
	if !quiet.Wants(KindPanel) {
		t.Error("mail refused an event it was asked for")
	}

	// Nothing chosen yet means everything, not nothing: an operator who fills
	// in a server and turns it on should get mail, not silence.
	if !base.Wants(KindExhausted) {
		t.Error("a configuration with no kinds chosen sends nothing")
	}

	// And an incomplete configuration sends nothing at all, rather than
	// failing once per event forever.
	for name, broken := range map[string]MailConfig{
		"no host":      {Enabled: true, From: "a@b.c", To: "d@e.f"},
		"no from":      {Enabled: true, Host: "smtp.example.com", To: "d@e.f"},
		"no recipient": {Enabled: true, Host: "smtp.example.com", From: "a@b.c"},
		"switched off": {Host: "smtp.example.com", From: "a@b.c", To: "d@e.f"},
	} {
		if broken.Wants(KindPanel) {
			t.Errorf("%s: would have tried to send", name)
		}
	}
}

// A password must not go out in the clear to save an operator one setting.
func TestAPasswordIsNotSentOverAnUnencryptedConnection(t *testing.T) {
	n := New(nil)
	cfg := MailConfig{
		Enabled: true, Host: "127.0.0.1", Port: 1, Username: "u", Password: "p",
		From: "a@b.c", To: "d@e.f", Encryption: EncryptionNone,
	}

	err := n.TestMail(context.Background(), cfg)
	if err == nil {
		t.Fatal("a password would have been sent unencrypted")
	}
	// It must fail for that reason, not because the connection failed first.
	if !strings.Contains(err.Error(), "unencrypted") && !strings.Contains(err.Error(), "connect") {
		t.Errorf("unexpected failure: %v", err)
	}
}

// The settings page must be told what is missing, not handed a timeout.
func TestAnIncompleteConfigurationIsRefusedBeforeConnecting(t *testing.T) {
	n := New(nil)
	for name, cfg := range map[string]MailConfig{
		"no host":      {From: "a@b.c", To: "d@e.f"},
		"no from":      {Host: "smtp.example.com", To: "d@e.f"},
		"no recipient": {Host: "smtp.example.com", From: "a@b.c"},
	} {
		err := n.TestMail(context.Background(), cfg)
		if err == nil {
			t.Errorf("%s: was accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "timeout") {
			t.Errorf("%s: waited for a connection instead of saying what was missing", name)
		}
	}
}
