package nodes

import (
	"testing"

	"github.com/abolfazl/w-ui/internal/service"
)

// What a node reports is charged against an allowance, so the arithmetic here
// is somebody's bill. A coefficient that silently came out as zero would serve
// traffic that never appears on any total.
func TestChargeReportChargesWhatTheNodeIsWorth(t *testing.T) {
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
			got, _ := chargeReport(reported, nil, tc.coefficient, nil)
			if len(got) != 1 {
				t.Fatalf("chargeReport() returned %d rows, want 1", len(got))
			}
			if got[0].Bytes != tc.wantBytes || got[0].Up != tc.wantUp || got[0].Down != tc.wantDown {
				t.Errorf("chargeReport(%g) = %d/%d/%d, want %d/%d/%d",
					tc.coefficient, got[0].Bytes, got[0].Up, got[0].Down,
					tc.wantBytes, tc.wantUp, tc.wantDown)
			}
			if got[0].OriginID != 5 {
				t.Errorf("chargeReport() lost the customer it belongs to")
			}
		})
	}
}

// Traffic that was counted must never scale away to nothing. A coefficient
// small enough to round a real transfer down to zero would let a customer use
// that node for free, one small transfer at a time.
func TestChargeReportNeverRoundsRealTrafficToNothing(t *testing.T) {
	got, _ := chargeReport([]service.NodeUsage{{OriginID: 1, Bytes: 10, Up: 4, Down: 6}}, nil, 0.001, nil)
	if got[0].Bytes == 0 || got[0].Up == 0 || got[0].Down == 0 {
		t.Errorf("chargeReport() charged nothing for traffic that happened: %+v", got[0])
	}
	if got[0].Bytes != got[0].Up+got[0].Down {
		t.Errorf("the total is not its two directions: %+v", got[0])
	}
}

// Nothing counted stays nothing. An idle customer must not accrue a byte per
// round for as long as their node is up.
func TestChargeReportLeavesIdleCustomersAlone(t *testing.T) {
	got, _ := chargeReport([]service.NodeUsage{{OriginID: 1}}, nil, 5, nil)
	if got[0].Bytes != 0 || got[0].Up != 0 || got[0].Down != 0 {
		t.Errorf("an idle customer was charged: %+v", got[0])
	}
}

// A customer's files on a node are charged at the node's price as well, and
// their rows add up to the customer's charged usage to the byte, whatever the
// coefficient does to the rounding.
func TestChargeReportKeepsTheFilesEqualToTheTotal(t *testing.T) {
	usage := []service.NodeUsage{{OriginID: 7, Bytes: 1001, Up: 333, Down: 668}}
	devices := []service.NodeDeviceUsage{
		{OriginID: 21, Up: 111, Down: 0},
		{OriginID: 22, Up: 111, Down: 334},
		{OriginID: 23, Up: 111, Down: 334},
	}
	clientOf := map[uint]uint{21: 7, 22: 7, 23: 7}
	for _, coefficient := range []float64{1, 1.5, 0.7, 3.3333} {
		got, files := chargeReport(usage, devices, coefficient, clientOf)
		var up, down uint64
		for _, f := range files {
			up += f.Up
			down += f.Down
		}
		if up != got[0].Up || down != got[0].Down {
			t.Errorf("x%g: files up %d down %d, customer up %d down %d",
				coefficient, up, down, got[0].Up, got[0].Down)
		}
		if got[0].Bytes != got[0].Up+got[0].Down {
			t.Errorf("x%g: customer total %d is not up+down %d", coefficient, got[0].Bytes, got[0].Up+got[0].Down)
		}
		for _, f := range files {
			if f.OriginID == 21 && f.Down != 0 {
				t.Errorf("x%g: a file that received nothing was charged %d", coefficient, f.Down)
			}
		}
	}
}

// Files an older node reported that do not add up to the customer's usage are
// converted each on their own, not forced onto a total they were not counted
// towards; files of a customer the report does not name are converted too.
func TestChargeReportConvertsLooseFilesOnTheirOwn(t *testing.T) {
	usage := []service.NodeUsage{{OriginID: 7, Bytes: 1000, Up: 0, Down: 1000}}
	devices := []service.NodeDeviceUsage{
		{OriginID: 21, Down: 300}, // adds up to 300, not 1000
		{OriginID: 30, Down: 50},  // a customer the report does not name
	}
	_, files := chargeReport(usage, devices, 2, map[uint]uint{21: 7, 30: 8})
	if len(files) != 2 || files[0].Down != 600 || files[1].Down != 100 {
		t.Fatalf("files = %+v, want 600 and 100", files)
	}
}

func TestShareIsExact(t *testing.T) {
	cases := []struct {
		total   uint64
		weights []uint64
	}{
		{10, []uint64{1, 1, 1}},
		{1 << 62, []uint64{1 << 61, 3, 1 << 40}},
		{7, []uint64{0, 5, 0}},
		{0, []uint64{4, 4}},
		{5, nil},
	}
	for _, c := range cases {
		got := share(c.total, c.weights)
		var sum, weights uint64
		for i, v := range got {
			sum += v
			weights += c.weights[i]
			if c.weights[i] == 0 && v != 0 {
				t.Errorf("share(%d, %v): a zero weight got %d", c.total, c.weights, v)
			}
		}
		if weights > 0 && sum != c.total {
			t.Errorf("share(%d, %v) = %v, sums to %d", c.total, c.weights, got, sum)
		}
	}
}
