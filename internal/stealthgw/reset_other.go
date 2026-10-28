//go:build !windows

package stealthgw

import "syscall"

// connReset is the error the system reads a reset connection as.
const connReset = syscall.ECONNRESET
