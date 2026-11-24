//go:build !unix

package main

// ownAsDataDir has nothing to do where files have no unix owner.
func ownAsDataDir(string, ...string) {}
