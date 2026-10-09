//go:build !linux

package l2tpagent

// InstallUnits has nothing to install where there is no systemd to run it.
func InstallUnits(binary, dataDir string) error { return errPlatform }
