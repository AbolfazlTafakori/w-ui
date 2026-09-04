package nodes

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/service"
)

// What a node reports is charged against an allowance, so the arithmetic here
// is somebody's bill. A coefficient that silently came out as zero would serve
// traffic that never appears on any total.
func TestScaleChargesWhatTheNodeIsWorth(t *testing.T) {
	reported := []service.NodeUsage{
		{OriginID: 5, Bytes: 1000, Up: 200, Down: 800},
	}

	cases := []struct {
		name              string
		coefficient       float64
		wantBytes, wantUp uint64
		wantDown          uint64
	}{
		{"an ordinary node charges what it counted", 1, 1000, 200, 800},
		{"an expensive node charges double", 2, 2000, 400, 1600},
		{"a cheap one can be discounted", 0.5, 500, 100, 400},
		// A node created before the column existed has zero, which is a missing
		// value and not an operator asking to give traffic away.
		{"zero is a node that predates this, not free traffic", 0, 1000, 200, 800},
		{"negative is nonsense and is ignored the same way", -3, 1000, 200, 800},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scale(reported, tc.coefficient)
			if len(got) != 1 {
				t.Fatalf("scale() returned %d rows, want 1", len(got))
			}
			if got[0].Bytes != tc.wantBytes || got[0].Up != tc.wantUp || got[0].Down != tc.wantDown {
				t.Errorf("scale(%g) = %d/%d/%d, want %d/%d/%d",
					tc.coefficient, got[0].Bytes, got[0].Up, got[0].Down,
					tc.wantBytes, tc.wantUp, tc.wantDown)
			}
			if got[0].OriginID != 5 {
				t.Errorf("scale() lost the customer it belongs to")
			}
		})
	}
}

// Traffic that was counted must never scale away to nothing. A coefficient
// small enough to round a real transfer down to zero would let a customer use
// that node for free, one small transfer at a time.
func TestScaleNeverRoundsRealTrafficToNothing(t *testing.T) {
	got := scale([]service.NodeUsage{{OriginID: 1, Bytes: 10, Up: 4, Down: 6}}, 0.001)
	if got[0].Bytes == 0 || got[0].Up == 0 || got[0].Down == 0 {
		t.Errorf("scale() charged nothing for traffic that happened: %+v", got[0])
	}
}

// Nothing counted stays nothing. An idle customer must not accrue a byte per
// round for as long as their node is up.
func TestScaleLeavesIdleCustomersAlone(t *testing.T) {
	got := scale([]service.NodeUsage{{OriginID: 1}}, 5)
	if got[0].Bytes != 0 || got[0].Up != 0 || got[0].Down != 0 {
		t.Errorf("an idle customer was charged: %+v", got[0])
	}
}
