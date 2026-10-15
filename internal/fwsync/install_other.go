//go:build !linux

package fwsync

import "errors"

// InstallUnits is Linux's.
func InstallUnits(string, string) error { return errors.New("fwsync: systemd units are Linux's") }
