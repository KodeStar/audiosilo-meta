// Package unionfind joins indexed values into components. Union always keeps the
// LOWEST index as a component's root, so a caller walking the indexes in order
// meets every component at its first member and can emit components in a stable,
// input-independent order - the order independence internal/audit's clusters and
// internal/importer's series resolution units rely on.
package unionfind

// Set is a union-find over the indexes 0..n-1.
type Set []int

// New is a Set of n singleton components.
func New(n int) Set {
	s := make(Set, n)
	for i := range s {
		s[i] = i
	}
	return s
}

// Find is the root of i's component: its lowest index.
func (s Set) Find(i int) int {
	for s[i] != i {
		s[i] = s[s[i]]
		i = s[i]
	}
	return i
}

// Union joins a's and b's components under the lower of their roots.
func (s Set) Union(a, b int) {
	ra, rb := s.Find(a), s.Find(b)
	if rb < ra {
		ra, rb = rb, ra
	}
	s[rb] = ra
}
