package config

import "testing"

// The owner's port is off unless set, and cannot be the panel's own.
func TestTheOwnersPortIsOptionalAndItsOwn(t *testing.T) {
	t.Setenv("WUI_LISTEN", "127.0.0.1:2096")
	t.Setenv("WUI_OWNER_LISTEN", "")
	c, err := Load()
	if err != nil || c.OwnerListen != "" {
		t.Fatalf("unset: %q %v", c.OwnerListen, err)
	}
	t.Setenv("WUI_OWNER_LISTEN", "127.0.0.1:2097")
	if c, err = Load(); err != nil || c.OwnerListen != "127.0.0.1:2097" {
		t.Fatalf("set: %q %v", c.OwnerListen, err)
	}
	t.Setenv("WUI_OWNER_LISTEN", "127.0.0.1:2096")
	if _, err := Load(); err == nil {
		t.Fatal("the owner's port the same as the panel's was accepted")
	}
}
