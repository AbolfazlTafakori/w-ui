//go:build linux

package enforce

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// benchTable is a table of n counters beside the panel's own, made for a
// benchmark and removed after it. Root only: it is the kernel being measured.
func benchTable(b *testing.B, n int) string {
	b.Helper()
	if os.Geteuid() != 0 {
		b.Skip("needs root: it measures the kernel's own tables")
	}
	name := fmt.Sprintf("wuibench%d", n)
	var s strings.Builder
	fmt.Fprintf(&s, "table inet %s {\n", name)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&s, "\tcounter c%d { packets 1 bytes 1500 }\n", i)
	}
	s.WriteString("}\n")
	_ = exec.Command("nft", "delete", "table", "inet", name).Run()
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(s.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("make the table: %v %s", err, out)
	}
	b.Cleanup(func() { _ = exec.Command("nft", "delete", "table", "inet", name).Run() })
	return name
}

// What a tick costs to drain the usage counters: every counter in the table,
// as it is done now, against only the counters of the files that moved.
func BenchmarkDrainCounters(b *testing.B) {
	for _, n := range []int{10_000, 50_000, 200_000} {
		table := benchTable(b, n)
		b.Run(fmt.Sprintf("all/counters=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := dumpObjectsIn(table, nftObjectCounter, true); err != nil {
					b.Fatal(err)
				}
			}
		})
		for _, moved := range []int{100, 1_000, 5_000} {
			names := make([]string, moved)
			for i := range names {
				names[i] = fmt.Sprintf("c%d", i*(n/moved))
			}
			b.Run(fmt.Sprintf("named/counters=%d/moved=%d", n, moved), func(b *testing.B) {
				for i := 0; i < b.N; i++ {
					if _, err := resetCountersIn(table, names); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
