package audit

import "testing"

// SetReviewedForTest lets the external integration test exercise repair.Run's own
// fresh Analyze with fixture decisions. This hook exists only in the test binary;
// production callers cannot replace or bypass the embedded policy.
func SetReviewedForTest(t testing.TB, raw []byte) {
	t.Helper()
	saved := reviewedDecisions
	reviewedDecisions = mustParseReviewed(raw)
	t.Cleanup(func() { reviewedDecisions = saved })
}
