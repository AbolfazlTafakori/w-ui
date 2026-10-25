//go:build !linux

package nftstate

import "errors"

// ListTables has no kernel to ask off Linux; the nft tool is asked instead.
func ListTables() ([]byte, error) { return nil, errors.New("nftstate: not on linux") }
