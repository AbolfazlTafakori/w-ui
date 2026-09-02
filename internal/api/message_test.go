package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/abolfazl/w-ui/internal/service"
)

// What an operator reads. A message that still carries the package that raised
// it is a message written for whoever wrote the code, and the operator has to
// look past two things they cannot use to reach the one they can.
func TestMessagesReadAsSentences(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("%w: listen port 99999 is out of range", service.ErrInvalid),
			"Listen port 99999 is out of range"},
		{fmt.Errorf("service: %w: name is required", service.ErrInvalid),
			"Name is required"},
		{errors.New("wgdriver: WireGuard is only available on Linux (running on windows)"),
			"WireGuard is only available on Linux (running on windows)"},
		{errors.New("ovpndriver: openvpn is not installed"),
			"Openvpn is not installed"},
		// Our own sentinels are markers for errors.Is, and the message after
		// them already says what they mean.
		{errors.New("service: device limit reached: Roya already has 1 of 1 devices"),
			"Roya already has 1 of 1 devices"},
		{errors.New("no addresses left on this interface: every address on tiny is in use"),
			"Every address on tiny is in use"},
		// A colon inside the message is not a prefix and must survive.
		{errors.New("could not reach it: Get \"http://127.0.0.1:9/api/system\""),
			"Could not reach it: Get \"http://127.0.0.1:9/api/system\""},
	}

	for _, c := range cases {
		if got := humanMessage(c.err); got != c.want {
			t.Errorf("humanMessage(%q)\n  got  %q\n  want %q", c.err, got, c.want)
		}
	}
}

func TestPackagePrefixesAreRecognisedNarrowly(t *testing.T) {
	// Only a bare lowercase word counts. Anything else is part of what the
	// operator is meant to read.
	for _, w := range []string{"service", "wgdriver", "enforce", "invalid input"} {
		if !isPackageWord(w) {
			t.Errorf("%q should be treated as a prefix", w)
		}
	}
	for _, w := range []string{"", "Get \"http", "Port 51820", "MTU", "a value"} {
		if isPackageWord(w) {
			t.Errorf("%q should not be stripped from a message", w)
		}
	}
}
