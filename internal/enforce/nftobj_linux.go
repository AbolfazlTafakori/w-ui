//go:build linux

package enforce

// Reading the table's counters and quotas from the kernel over netlink.
//
// Every tick reads them, and the nft tool is a poor way to: before it lists
// a single counter it reads the whole table back from the kernel -- every
// customer's chains and rules -- so its cost grows with the table, not with
// what was asked. On a server with ten thousand customers each read took
// most of a second, two of them every two-second tick, and the loop fell
// behind with the panel's own CPU at a third of one core. Asked directly,
// the kernel answers with the objects alone.
//
// The nft tool stays the fallback: a kernel or a sandbox that refuses this
// gets the same answer the slow way.

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/mdlayher/netlink"
	"golang.org/x/sys/unix"
)

// From linux/netfilter/nfnetlink.h and linux/netfilter/nf_tables.h.
const (
	nfnlSubsysNFTables = 10
	nftMsgGetObj       = 19
	nftMsgGetObjReset  = 21

	nftaObjTable = 1
	nftaObjName  = 2
	nftaObjType  = 3
	nftaObjData  = 4

	nftObjectCounter = 1
	nftObjectQuota   = 2

	nftaCounterBytes  = 1
	nftaQuotaConsumed = 4

	nfprotoINet = 1
)

// errNoTable is a table that is not there, as nft says it.
var errNoTable = errors.New("No such file or directory")

// dumpObjects lists the table's objects of one type -- its counters, or its
// quotas -- each with its value, and with reset zeroes each as it is read,
// so nothing that arrives between reading and zeroing is lost.
func dumpObjects(objType uint32, reset bool) ([]namedValue, error) {
	c, err := netlink.Dial(unix.NETLINK_NETFILTER, nil)
	if err != nil {
		return nil, fmt.Errorf("netlink: %w", err)
	}
	defer c.Close()

	ae := netlink.NewAttributeEncoder()
	ae.ByteOrder = binary.BigEndian
	ae.String(nftaObjTable, TableName)
	ae.Uint32(nftaObjType, objType)
	attrs, err := ae.Encode()
	if err != nil {
		return nil, err
	}

	msg := uint16(nftMsgGetObj)
	if reset {
		msg = nftMsgGetObjReset
	}
	// nfgenmsg: the family, the version, and a resource id of 0.
	data := append([]byte{nfprotoINet, 0, 0, 0}, attrs...)
	msgs, err := c.Execute(netlink.Message{
		Header: netlink.Header{
			Type:  netlink.HeaderType(nfnlSubsysNFTables<<8 | msg),
			Flags: netlink.Request | netlink.Dump,
		},
		Data: data,
	})
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, errNoTable
		}
		return nil, fmt.Errorf("netlink: %w", err)
	}

	out := make([]namedValue, 0, len(msgs))
	for _, m := range msgs {
		if len(m.Data) < 4 {
			continue
		}
		o, ok, err := parseObject(m.Data[4:], objType)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, o)
		}
	}
	return out, nil
}

// parseObject reads one object of a dump: its name, and its counter's bytes
// or its quota's consumption.
func parseObject(b []byte, objType uint32) (namedValue, bool, error) {
	ad, err := netlink.NewAttributeDecoder(b)
	if err != nil {
		return namedValue{}, false, err
	}
	ad.ByteOrder = binary.BigEndian
	var o namedValue
	var typ uint32
	var data []byte
	for ad.Next() {
		switch ad.Type() {
		case nftaObjName:
			o.name = ad.String()
		case nftaObjType:
			typ = ad.Uint32()
		case nftaObjData:
			data = ad.Bytes()
		}
	}
	if err := ad.Err(); err != nil {
		return namedValue{}, false, err
	}
	if typ != objType || o.name == "" {
		return namedValue{}, false, nil
	}
	want := uint16(nftaCounterBytes)
	if objType == nftObjectQuota {
		want = nftaQuotaConsumed
	}
	dd, err := netlink.NewAttributeDecoder(data)
	if err != nil {
		return namedValue{}, false, err
	}
	dd.ByteOrder = binary.BigEndian
	for dd.Next() {
		if dd.Type() == want {
			o.value = dd.Uint64()
		}
	}
	return o, true, dd.Err()
}
