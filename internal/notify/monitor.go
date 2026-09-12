package notify

import (
	"context"
	"fmt"
	"time"
)

// RunSystemMonitor raises the CPU and memory events. Each fires once when
// the reading crosses its threshold and is armed again when it drops well
// below, so a host hovering at the line does not send a message a minute.
func (n *Notifier) RunSystemMonitor(ctx context.Context, sample func() (cpu, mem float64)) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		var cpuHigh, memHigh bool
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			th := n.Config().Thresholds
			cpu, mem := sample()
			cpuHigh = n.crossing(cpuHigh, cpu, th.CPU, KindCPUHigh, "CPU")
			memHigh = n.crossing(memHigh, mem, th.Memory, KindMemoryHigh, "Memory")
		}
	}()
}

func (n *Notifier) crossing(high bool, value float64, threshold int, kind Kind, what string) bool {
	if threshold <= 0 {
		return false
	}
	switch {
	case !high && value >= float64(threshold):
		n.Send(Event{
			Kind:  kind,
			Title: what + " usage is high",
			Body:  fmt.Sprintf("%s is at %.0f%%, over the %d%% threshold", what, value, threshold),
		})
		return true
	case high && value < float64(threshold)-10:
		return false
	}
	return high
}
