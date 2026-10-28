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
// wui-udp is Gateway and Client; wui-ws is StreamGateway and StreamClient;
// wui-tls is TLSGateway and StreamClient, with ClientTLS; wui-poll is
// PollGateway and PollClient; wui-plain is PlainGateway and StreamClient;
// wui-shape is ShapeGateway (its reference client comes next).
// docs/reference/stealth.md is the specification.
package stealthgw
