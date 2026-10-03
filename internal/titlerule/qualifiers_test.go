package titlerule

import (
	"slices"
	"strings"
	"testing"
)

// The two product qualifiers and the brand possessive are COMPARISON rules first:
// what each pins is which pairs of titles meet on the identity key and which do not.
func TestTitleQualifiersMeetTheirPlainTwin(t *testing.T) {
	cases := []struct {
		name   string
		plain  string
		decor  string
		series string
		same   bool
	}{
		// The marketplace edition, in every spelling the tree and the dump carry.
		{name: "a colon segment", plain: "The Search", decor: "The Search: International Edition", same: true},
		{name: "a quoted run", plain: "Cold Fire", decor: `Cold Fire "International Edition"`, same: true},
		{name: "a quoted plural", plain: "The World Wreckers", decor: `The World Wreckers "International Editions"`, same: true},
		{name: "curly quotes", plain: "Cold Fire", decor: "Cold Fire “International Edition”", same: true},
		{name: "a bracketed group", plain: "Outlander", decor: "Outlander (International Edition)", same: true},
		{name: "quoted inside brackets", plain: "Next to Last Stand", decor: `Next to Last Stand ("International Edition")`, same: true},
		{
			name:  "a quoted run before a production decoration",
			plain: "Change of Command [Dramatized Adaptation]", decor: `Change of Command "International Edition" [Dramatized Adaptation]`,
			same: true,
		},
		{
			name:  "a segment followed by another segment keeps that segment",
			plain: "Voyager, Parts 1 and 2", decor: "Voyager: International Edition, Parts 1 and 2",
			same: true,
		},
		// ... and the shapes that are not the marker.
		{name: "the phrase opening a longer segment", plain: "Atlas", decor: "Atlas: International Editions of the Classics", same: false},
		{name: "a later segment after an earlier use of the words", plain: "Atlas: International Editions of the Classics", decor: "Atlas: International Editions of the Classics: International Edition", same: true},
		{name: "the phrase with no separator before it", plain: "The Times", decor: "The Times International Edition", same: false},

		// The narrator qualifier: a bracketed group, or the title's last segment.
		{name: "a bracketed narrator", plain: "Lock In", decor: "Lock In (Narrated by Wil Wheaton)", same: true},
		{name: "a dash segment, German", plain: "Harry Potter und der Feuerkelch", decor: "Harry Potter und der Feuerkelch - Gesprochen von Rufus Beck", same: true},
		{name: "a full-stop segment", plain: "Die Bibel", decor: "Die Bibel. Gelesen von Rufus Beck", same: true},
		{name: "a comma segment", plain: "ESV Audio Bible", decor: "ESV Audio Bible, Read by Ray Ortlund", same: true},
		{name: "two narrators", plain: "Just So Stories", decor: "Just So Stories, Read by Tony Robinson and Miriam Margolyes", same: true},
		{name: "a bracketed narration inside a later segment", plain: "Atlas: Complete", decor: "Atlas (Read by Jane Doe): Complete", same: true},
		// ... and the shapes that are not a credit and nothing else.
		{name: "a leading lead-in names a book", plain: "How to Produce an Audiobook on a Budget", decor: "Narrated by the Author: How to Produce an Audiobook on a Budget", same: false},
		{name: "no separator before the lead-in", plain: "A Christmas Carol: The Classic", decor: "A Christmas Carol: The Classic Narrated by Chandler Craig", same: false},
		{name: "a credit naming nobody", plain: "The Life of Josiah Henson", decor: "The Life of Josiah Henson, as Narrated by Himself", same: false},
		{name: "a credit carrying the volume number", plain: "Tarzan", decor: "Tarzan - Narrated by William Martin 2", same: false},
		{name: "a lead-in inside a title", plain: "A Wedding Romance", decor: "A Read by the Sea Wedding Romance", same: false},
		// A lead-in at a segment boundary followed by prose rather than a name.
		{name: "read by an object, not a name", plain: "Murder", decor: "Murder: Read by Candlelight", same: false},
		{name: "read by an object after a full stop", plain: "Stop", decor: "Stop. Read by Moonlight", same: false},
		{name: "narrated by a character", plain: "The Book Thief", decor: "The Book Thief: Narrated by Death", same: false},
		{name: "a lowercase credit is prose", plain: "Night Poems", decor: "Night Poems, Read by candle and lamp", same: false},
		{name: "a credit too long to be one", plain: "Deutsche Gedichte", decor: "Deutsche Gedichte - Gelesen von Ulrich Tukur und Christian Redl am Klavier", same: false},

		// The brand possessive.
		{name: "a brand with and without its possessive", plain: "Tom Clancy Oath of Office", decor: "Tom Clancy's Oath of Office", same: true},
		{name: "a curly apostrophe", plain: "Clive Cussler The Heist", decor: "Clive Cussler’s The Heist", same: true},
		{
			name:  "the brand meets a series-decorated twin",
			plain: "Tom Clancy Oath of Office: Jack Ryan Novel Series, Book 19", series: "Jack Ryan",
			decor: "Tom Clancy's Oath of Office", same: true,
		},
		{name: "an article is no brand", plain: "A Doll House", decor: "A Doll's House", same: false},
		{name: "one word is no brand", plain: "Dragon Magic", decor: "Dragon's Magic", same: false},
		{name: "a possessive past the head is untouched", plain: "Old Man War", decor: "The Old Man's War", same: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := IdentityTitleKey(c.plain, c.series), IdentityTitleKey(c.decor, "")
			if a == "" || b == "" {
				t.Fatalf("empty key: %q -> %q, %q -> %q", c.plain, a, c.decor, b)
			}
			if (a == b) != c.same {
				t.Errorf("IdentityTitleKey(%q) = %q and (%q) = %q: same = %v, want %v", c.plain, a, c.decor, b, a == b, c.same)
			}
		})
	}
}

// A qualifier is a DECORATION, so the table files it and the retitle strips it - which
// is what makes a marketplace-edition twin the clean twin W-DUP's survivor rule looks
// for, and what lets the intake gate compose a submitted "X: International Edition"
// as "X".
func TestTitleQualifiersAreDecorations(t *testing.T) {
	cases := []struct {
		title, code, proposed string
	}{
		{"The Search: International Edition", DecMarketEdition, "The Search"},
		{`Cold Fire "International Edition"`, DecMarketEdition, "Cold Fire"},
		{"Outlander (International Edition)", DecMarketEdition, "Outlander"},
		{"ESV Audio Bible, Read by Ray Ortlund", DecNarrator, "ESV Audio Bible"},
		{"Harry Potter und der Feuerkelch - Gesprochen von Rufus Beck", DecNarrator, "Harry Potter und der Feuerkelch"},
		{"Minecraft: The Island (Narrated by Jack Black)", DecNarrator, "Minecraft: The Island"},
		{`Next to Last Stand ("International Edition")`, DecMarketEdition, "Next to Last Stand"},
		// A group removed from before a separator takes its space with it.
		{"Voice Only Audio Bible - NKJV (Narrated by Bob Souer): Complete Bible", DecNarrator,
			"Voice Only Audio Bible - NKJV: Complete Bible"},
	}
	for _, c := range cases {
		codes := Decorations(TitleFacts{Title: c.title})
		if !slices.Contains(codes, c.code) {
			t.Errorf("Decorations(%q) = %v, want it to hold %s", c.title, codes, c.code)
		}
		if got, ok := ProposeTitle(c.title, ""); !ok || got != c.proposed {
			t.Errorf("ProposeTitle(%q) = %q, %v; want %q", c.title, got, ok, c.proposed)
		}
	}
	for _, title := range []string{
		"Narrated by the Author: How to Produce an Audiobook on a Budget",
		"Tarzan - Narrated by William Martin 2",
		"The Times International Edition",
		"Murder: Read by Candlelight",
		"Stop. Read by Moonlight",
		"The Book Thief: Narrated by Death",
		"Tom Clancy's Oath of Office", // the brand fold is a comparison rule, never a retitle
	} {
		codes := Decorations(TitleFacts{Title: title})
		if slices.Contains(codes, DecMarketEdition) || slices.Contains(codes, DecNarrator) {
			t.Errorf("Decorations(%q) = %v, want no qualifier code", title, codes)
		}
	}
}

// NarratorLeadIns is matched case-insensitively against ASCII and read by
// internal/importer in its foldCredit form, so every entry must already be lowercase
// ASCII.
func TestNarratorLeadInsAreFolded(t *testing.T) {
	for _, lead := range trailingLeadIns {
		if lead != strings.ToLower(lead) || strings.TrimSpace(lead) != lead {
			t.Errorf("lead-in %q is not lowercase and trimmed", lead)
		}
		for _, r := range lead {
			if r > 127 {
				t.Errorf("lead-in %q is not ASCII", lead)
			}
		}
	}
}

// A title that is nothing but a qualifier keeps itself: the caller's own fallback
// judges it, never an empty string.
func TestTitleQualifiersNeverEmptyATitle(t *testing.T) {
	for _, title := range []string{`"International Edition"`, "(Narrated by Jane Doe)"} {
		if got := dropTitleQualifiers(title); got != title {
			t.Errorf("dropTitleQualifiers(%q) = %q, want it unchanged", title, got)
		}
	}
}

// The brand fold must not break the series strip: a series whose own name carries the
// possessive still comes off its volumes, so "Tom Clancy's Op-Center: Dark Zone" read
// against "Tom Clancy's Op-Center" meets the plain "Dark Zone" - and a series spelled
// without it still comes off a title that carries it.
func TestBrandFoldKeepsTheSeriesStrip(t *testing.T) {
	cases := []struct{ title, series, want string }{
		{"Tom Clancy's Op-Center: Dark Zone", "Tom Clancy's Op-Center", "Dark Zone"},
		{"Tom Clancy's Op-Center: Dark Zone", "Tom Clancy Op-Center", "Dark Zone"},
		{"Tom Clancy's Oath of Office", "Jack Ryan", "Tom Clancy Oath of Office"},
	}
	for _, c := range cases {
		if got := Clean(c.title, c.series); got != c.want {
			t.Errorf("Clean(%q, %q) = %q, want %q", c.title, c.series, got, c.want)
		}
	}
}

// A title that is nothing but a qualifier is not FILED as carrying one either: the
// decoration table asks the same never-empty question the strip does.
func TestABareQualifierIsNoDecoration(t *testing.T) {
	for _, title := range []string{`"International Edition"`, "(Narrated by Jane Doe)", "(International Edition)"} {
		codes := Decorations(TitleFacts{Title: title})
		if slices.Contains(codes, DecMarketEdition) || slices.Contains(codes, DecNarrator) {
			t.Errorf("Decorations(%q) = %v, want no qualifier code", title, codes)
		}
	}
}
