//go:build linux

package single

import (
	"errors"
	"net"
	"syscall"
	"time"
)

// The claim is an abstract unix socket, and the choice matters.
//
// A lock file would have been the obvious thing and is wrong twice over. The
// panel runs under a unit with ProtectSystem=strict and one writable path, so
// it cannot create a lock anywhere a second panel with a different data
// directory would also look — and a lock inside the data directory is no lock
// at all, because the whole point is to catch the panel that was installed
// somewhere else. A file also outlives the process that made it, so a kill -9
// leaves a lock nobody holds.
//
// An abstract socket has neither problem: it needs no filesystem write
// permission, and the kernel releases it when the process dies, however it
// dies.
//
// It is also scoped exactly right. Abstract sockets live in a network
// namespace, and so do nftables tables, tc qdiscs and network interfaces —
// which are the things being fought over. Two panels in two containers on one
// host have separate namespaces, separate rulesets and no conflict, and this
// lets them run. Two in the same namespace are the case that breaks, and it
// refuses them.
const socketName = "@wui-panel"

// answerTimeout bounds asking the holder who it is. It is a local socket, so
// this is only ever about a holder that has wedged.
const answerTimeout = 2 * time.Second

type listenerHolder struct {
	ln   net.Listener
	done chan struct{}
}

func (h *listenerHolder) Release() {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
	_ = h.ln.Close()
}

func claim(identity string) (Holder, error) {
	ln, err := net.Listen("unix", socketName)
	if err != nil {
		if isAddrInUse(err) {
			return nil, Describe(ErrAlreadyRunning, ask())
		}
		// Anything else — a kernel without unix sockets, a seccomp policy —
		// must not stop the panel starting. The guard is worth having and is
		// not worth being the reason a server has no panel.
		return noopHolder{}, nil
	}

	h := &listenerHolder{ln: ln, done: make(chan struct{})}

	// Answering is the whole reason this is a socket rather than a lock: the
	// panel that is refused can say which one is in its way.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-h.done:
					return
				default:
					return
				}
			}
			_ = conn.SetWriteDeadline(time.Now().Add(answerTimeout))
			_, _ = conn.Write([]byte(identity))
			_ = conn.Close()
		}
	}()

	return h, nil
}

// ask connects to the holder and reads how it describes itself. An empty answer
// is not a failure: the refusal stands either way, and a vaguer message is
// better than not refusing.
func ask() string {
	conn, err := net.DialTimeout("unix", socketName, answerTimeout)
	if err != nil {
		return ""
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(answerTimeout))
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil && n == 0 {
		return ""
	}
	return string(buf[:n])
}

func isAddrInUse(err error) bool {
	var se syscall.Errno
	if errors.As(err, &se) {
		return se == syscall.EADDRINUSE
	}
	return false
}

type noopHolder struct{}

func (noopHolder) Release() {}
