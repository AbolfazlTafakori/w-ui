//go:build !linux

package single

// Claiming the machine is a Linux idea, and so is everything it protects.
//
// The things two panels fight over — nftables tables, tc hierarchies, WireGuard
// devices — do not exist off Linux, where the panel runs with inert drivers for
// development. There is nothing to protect and nothing to refuse.
func claim(string) (Holder, error) { return noopHolder{}, nil }

type noopHolder struct{}

func (noopHolder) Release() {}
