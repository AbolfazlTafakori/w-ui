package idset

import (
	"slices"
	"testing"
)

func TestUniqueKeepsTheFirstOfEachAndNoZero(t *testing.T) {
	if got := Unique([]uint{3, 0, 1, 3, 2, 1}); !slices.Equal(got, []uint{3, 1, 2}) {
		t.Fatalf("got %v", got)
	}
}

func TestCombineAddsAndTakesAwayInAStableOrder(t *testing.T) {
	if got := Combine([]uint{1, 2}, []uint{3, 1}, true); !slices.Equal(got, []uint{1, 2, 3}) {
		t.Fatalf("add: got %v", got)
	}
	if got := Combine([]uint{1, 2, 3}, []uint{2, 9}, false); !slices.Equal(got, []uint{1, 3}) {
		t.Fatalf("remove: got %v", got)
	}
}

func TestSameIgnoresOrderButNotCount(t *testing.T) {
	if !Same([]uint{1, 2, 3}, []uint{3, 1, 2}) {
		t.Fatal("same ids in another order")
	}
	if Same([]uint{1, 1, 2}, []uint{1, 2, 2}) {
		t.Fatal("different counts")
	}
	if Same([]uint{1}, []uint{1, 2}) {
		t.Fatal("different lengths")
	}
}
