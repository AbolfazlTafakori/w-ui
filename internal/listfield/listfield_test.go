package listfield

import (
	"strings"
	"testing"
)

func TestSplitTakesWhatAnOperatorPastes(t *testing.T) {
	got := strings.Join(Split(" a.com,b.com\nc.com\r\n a.com\t d.com ,,"), "|")
	if got != "a.com|b.com|c.com|d.com" {
		t.Fatalf("Split = %s", got)
	}
	if Join([]string{" x ", "y", "x", ""}) != "x,y" {
		t.Fatal("Join does not trim and drop repeats")
	}
}
