package api

import "testing"

// A disk is short under 2 GB free, worth a look under 5% of it (at least 10 GB, at most 200 GB, so a
// big array with terabytes free isn't flagged).
func TestLowOnSpace(t *testing.T) {
	const gb = 1 << 30
	for _, c := range []struct {
		free, total uint64
		want        string
	}{
		{1 * gb, 100 * gb, "problem"},
		{8 * gb, 100 * gb, "warn"},
		{40 * gb, 100 * gb, ""},
		{150 * gb, 12000 * gb, "warn"},
		{500 * gb, 12000 * gb, ""},
		{30 * gb, 400 * gb, ""},
		{15 * gb, 400 * gb, "warn"},
	} {
		if got := lowOn(diskRoom{free: c.free, total: c.total}); got != c.want {
			t.Errorf("%d GB free of %d GB = %q, want %q", c.free/gb, c.total/gb, got, c.want)
		}
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 5523: "5,523", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
