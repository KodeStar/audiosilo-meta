package audit

// unionFind joins indexed values into components, always keeping the lowest index
// as the root so callers can emit components in a stable order.
type unionFind []int

func newUnionFind(n int) unionFind {
	parents := make(unionFind, n)
	for i := range parents {
		parents[i] = i
	}
	return parents
}

func (u unionFind) find(i int) int {
	for u[i] != i {
		u[i] = u[u[i]]
		i = u[i]
	}
	return i
}

func (u unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if rb < ra {
		ra, rb = rb, ra
	}
	u[rb] = ra
}
