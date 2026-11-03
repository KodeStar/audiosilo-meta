package importer

import "testing"

// TestLibrivoxPartRowIsNotTheWhole pins the writer half of pkg/check's part rule
// (partsOfAWhole): a LibriVox part project - "Kafka's Book, Book 5" - keys with the
// catalogued whole, and the census no longer pairs the two, so the create guard
// must not refuse it as the whole's duplicate either. It does not, through its own
// rules rather than the census's: the row's title STATES a volume, and the
// positive position veto (dupidentity.go) refuses a stating row only when the
// catalogue places the matched work at that volume - which nothing does for a
// LibriVox part. So it holds whether or not sibling parts are catalogued.
func TestLibrivoxPartRowIsNotTheWhole(t *testing.T) {
	for name, works := range map[string]map[string][]string{
		"beside the whole alone": {"kafkas-book": {"Kafka's Book", "franz-kafka"}},
		"beside the whole and two parts": {
			"kafkas-book":        {"Kafka's Book", "franz-kafka"},
			"kafkas-book-book-1": {"Kafka's Book, Book 1", "franz-kafka"},
			"kafkas-book-book-2": {"Kafka's Book, Book 2", "franz-kafka"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedWorks(t, dataDir, works)
			sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99520, "Kafka's Book, Book 5")), dataDir, allLibrivox)
			if sum.NewWorks != 1 || sum.SkippedDuplicateIdentity != 0 || len(sum.LibrivoxWorklist) != 0 {
				t.Fatalf("new works/refused as duplicate/worklist = %d/%d/%v\nwarnings: %v",
					sum.NewWorks, sum.SkippedDuplicateIdentity, sum.LibrivoxWorklist, sum.Warnings)
			}
			if !entryExists(t, dataDir, workAddr("kafkas-book-book-5")) {
				t.Errorf("the part was not created at kafkas-book-book-5")
			}
		})
	}
}
