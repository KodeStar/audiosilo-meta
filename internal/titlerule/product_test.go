package titlerule

import "testing"

// The split-release part forms: the measured catalogue spellings, the German
// and square-bracket forms, and the retailer's series count that is NOT a part.
func TestPartOf(t *testing.T) {
	for _, c := range []struct {
		title      string
		num, total int
		ok         bool
	}{
		{"The Blood Mirror (2 of 2) [Dramatized Adaptation]", 2, 2, true},
		{"Morning Star (Part 1 of 2) (Dramatized Adaptation)", 1, 2, true},
		{"The Broken Eye ( 3 of 3) [Dramatized Adaptation]", 3, 3, true},
		{"The Earth Died Screaming, Vol. 1 of 2 (Dramatized Adaptation)", 1, 2, true},
		{"Der Weg der Könige [Teil 2 von 3]", 2, 3, true},
		{"Blood Stained: The Legend of Andrew Rufus (Book 3 of 7)", 0, 0, false},
		{"1984", 0, 0, false},
	} {
		num, total, ok := PartOf(c.title)
		if ok != c.ok || num != c.num || total != c.total {
			t.Errorf("PartOf(%q) = %d,%d,%v; want %d,%d,%v", c.title, num, total, ok, c.num, c.total, c.ok)
		}
	}
	if !IsSplitPart("The Lost Coast, Part 1") || IsSplitPart("The Lost Coast, Volume 2") {
		t.Error("a Part marker is a split part; a Volume marker is a series volume")
	}
}

// ProductOf reads every variant, and each statement is its own field.
func TestProductOf(t *testing.T) {
	plain := ProductOf("Cartographer Chronicles", "The Lost Coast")
	if plain != (Product{}) {
		t.Errorf("a plain title states nothing: %+v", plain)
	}
	if p := ProductOf("", "The Lost Coast", "Young Readers Edition"); !p.Adapted {
		t.Errorf("a young-readers subtitle is an adapted edition: %+v", p)
	}
	if p := ProductOf("", "Tangled: The Series"); !p.Adapted {
		t.Errorf("a \"The Series\" edition is an adapted edition: %+v", p)
	}
	if p := ProductOf("", "The Lost Coast (1 of 2)"); !p.Part {
		t.Errorf("a part count is a split part: %+v", p)
	}
	if p := ProductOf("", "The Cartographer Box Set: Books 1-3"); !p.Collection {
		t.Errorf("a box set is a collection: %+v", p)
	}
}
