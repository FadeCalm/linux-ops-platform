package update

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.1", "0.1.0", 1},
		{"0.1.0", "0.1.1", -1},
		{"0.1.0", "0.1.0", 0},
		{"v0.2.0", "0.1.9", 1},
		{"1.0", "1.0.0", 0},
		{"1.0.1-rc1", "1.0.1", 0},
		{"1.2.10", "1.2.9", 1},
		{"", "0.0.1", -1},
	}
	for _, c := range cases {
		got := Compare(c.a, c.b)
		if got != c.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseVersionGarbage(t *testing.T) {
	if got := parseVersion("abc"); got != [3]int{} {
		t.Fatalf("parseVersion(abc) = %v, want zero", got)
	}
	if got := parseVersion("2.x.3"); got != [3]int{2, 0, 3} {
		t.Fatalf("parseVersion(2.x.3) = %v, want [2 0 3]", got)
	}
}
