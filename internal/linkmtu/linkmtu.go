// Package linkmtu says how big a packet this server's way out to the
// internet carries: the MTU of the device its default route leaves by.
//
// A tunnel's packets are the customer's packets with the tunnel's own
// header around them, and AmneziaWG pads them further; one that comes out
// bigger than the link is split in two on the way, which costs every
// customer speed and which some networks drop outright. Most servers sit
// on a 1500-byte link, but not all of them -- cloud networks with an
// overlay of their own hand out 1450 or 1400 -- and a profile sized for
// 1500 is then too big on every packet.
package linkmtu

// Default is what a link is taken to carry when it cannot be read: an
// Ethernet link's, what almost every server has.
const Default = 1500

// Lowest is the least a link is taken to carry, IPv6's minimum: a reading
// below it is a device that is not the way out.
const Lowest = 1280
