package unionfind

import "testing"

// The root is always the lowest index, whatever order the unions arrive in.
func TestUnionKeepsTheLowestRoot(t *testing.T) {
	for _, pairs := range [][][2]int{{{3, 1}, {4, 3}, {2, 0}}, {{4, 3}, {2, 0}, {1, 3}}} {
		s := New(5)
		for _, p := range pairs {
			s.Union(p[0], p[1])
		}
		for i, want := range []int{0, 1, 0, 1, 1} {
			if got := s.Find(i); got != want {
				t.Errorf("unions %v: Find(%d) = %d, want %d", pairs, i, got, want)
			}
		}
	}
}
