// Package stealthgw is the server of W-UI's stealth transports, and the
// reference client they are tested against.
//
// A gateway stands in front of one WireGuard tunnel. It takes the wrapped
// packets of package stealth off its own port, keeps every rule of the
// wrapping -- silence for strangers, the replay rule, the limits, the timers
// -- and hands what is inside to the tunnel's own WireGuard, which keeps all
// of its rules. Nothing about the tunnel changes for it: the kernel module or
// the userspace daemon runs as it would on plain UDP.
//
// veil is Gateway and Client; drift is StreamGateway and StreamClient;
// anchor is TLSGateway and StreamClient, with ClientTLS; relay is
// PollGateway and PollClient; bare is PlainGateway and StreamClient;
// mold is ShapeGateway and ShapeClient.
// docs/reference/stealth.md is the specification.
package stealthgw
