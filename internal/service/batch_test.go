package service

import "testing"

// A batch is named by its method: a running number after the prefix, a
// random handle alone, or prefix, handle and postfix.
func TestBatchNames(t *testing.T) {
	n, _ := batchName(BatchInput{Method: batchPrefixNumber, Prefix: "shop-", Start: 5, Postfix: "-x"}, 2)
	if n != "shop-7-x" {
		t.Fatalf("prefix and number: %q", n)
	}
	n, _ = batchName(BatchInput{Method: batchRandom}, 0)
	if len(n) != 10 {
		t.Fatalf("a random handle is ten characters: %q", n)
	}
	n, _ = batchName(BatchInput{Method: batchPrefixRandom, Prefix: "a_", Postfix: "_z"}, 0)
	if len(n) != 12 || n[:2] != "a_" || n[10:] != "_z" {
		t.Fatalf("prefix, handle and postfix: %q", n)
	}
	n, _ = batchName(BatchInput{Prefix: "p"}, 0)
	if n != "p1" {
		t.Fatalf("the default is prefix and number from 1: %q", n)
	}
}
