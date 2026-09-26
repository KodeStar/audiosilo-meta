package importer

import (
	"testing"
)

func TestDecodeHTMLEntities(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`The Doomsday Key &quot;International Edition&quot;`, `The Doomsday Key "International Edition"`},
		{"Shopaholic &amp; Sister", "Shopaholic & Sister"},
		{"Don&#39;t Look Back", "Don't Look Back"},
		{"Ingenier&iacute;a interior", "Ingeniería interior"},
		{"It&#x2019;s Complicated", "It’s Complicated"},
		{"Caf&Eacute; &ndash; Two", "CafÉ – Two"},
		// A decoded no-break space is an ordinary space.
		{"Nine&nbsp;Lives", "Nine Lives"},
		{"Nine&#160;Lives", "Nine Lives"},
		// A literal ampersand is text, with or without spaces round it.
		{"Tom & Jerry", "Tom & Jerry"},
		{"Q&A", "Q&A"},
		// The legacy semicolon-less references are NOT read: a bare ampersand
		// in a title is an ampersand, whatever letters follow it.
		{"Rock&regular", "Rock&regular"},
		{"Fish&notes &amp chips", "Fish&notes &amp chips"},
		// An unknown name is left exactly as written.
		{"AT&T; Stories", "AT&T; Stories"},
		// ONE pass: a doubly-escaped value decodes once.
		{"Tom &amp;amp; Jerry", "Tom &amp; Jerry"},
		{"", ""},
	} {
		if got := DecodeHTMLEntities(tc.in); got != tc.want {
			t.Errorf("DecodeHTMLEntities(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLibexEntityTextImportsDecoded is the defect itself: libex hands its text
// over with literal references, and every free-text field - title, subtitle,
// publisher, series name, credits, chapter titles - must reach the record and
// the slug decoded.
func TestLibexEntityTextImportsDecoded(t *testing.T) {
	row := `{"asin":"B0BS4HL89H","title":"The Doomsday Key &quot;International Edition&quot;",` +
		`"subtitle":"A Sigma Force Novel &amp; More","region":"us","language":"english","bookFormat":"unabridged",` +
		`"releaseDate":"2020-01-01 00:00:00+00","lengthMinutes":600,"publisher":"Simon &amp; Schuster Audio",` +
		`"authors":[{"name":"Jos&eacute; Rollins"}],"narrators":[{"name":"Christian&nbsp;Baskous"}],` +
		`"series":[{"name":"Rizzoli &amp; Isles","position":"1"}],` +
		`"chapters":[{"title":"Tom &amp; Jerry","startOffsetMs":0,"lengthMs":1000}]}`
	sum, dataDir := runLibex(t, row, false)
	if sum.NewWorks != 1 {
		t.Fatalf("NewWorks = %d, want 1: %+v", sum.NewWorks, sum)
	}
	const workSlug = "the-doomsday-key-international-edition"
	var work struct {
		Title   string
		Authors []string
	}
	readEntity(t, dataDir, workAddr(workSlug), &work)
	if work.Title != `The Doomsday Key "International Edition"` {
		t.Errorf("work title = %q, want the decoded text", work.Title)
	}
	if len(work.Authors) != 1 || work.Authors[0] != "jose-rollins" {
		t.Errorf("authors = %v, want [jose-rollins]", work.Authors)
	}
	var person struct{ Name string }
	readEntity(t, dataDir, personAddr("jose-rollins"), &person)
	if person.Name != "José Rollins" {
		t.Errorf("author name = %q", person.Name)
	}
	readEntity(t, dataDir, personAddr("christian-baskous"), &person)
	if person.Name != "Christian Baskous" {
		t.Errorf("narrator name = %q, want the no-break space read as a space", person.Name)
	}
	recs := recSlugsOf(t, dataDir, workSlug)
	if len(recs) != 1 {
		t.Fatalf("recordings = %v, want one", recs)
	}
	var rec struct {
		Publisher string
		Chapters  []struct{ Title string }
	}
	readEntity(t, dataDir, recAddr(workSlug, recs[0]), &rec)
	if rec.Publisher != "Simon & Schuster Audio" {
		t.Errorf("publisher = %q", rec.Publisher)
	}
	if len(rec.Chapters) != 1 || rec.Chapters[0].Title != "Tom & Jerry" {
		t.Errorf("chapters = %+v, want the decoded chapter title", rec.Chapters)
	}
	var series struct{ Name string }
	readEntity(t, dataDir, seriesAddr("rizzoli-isles"), &series)
	if series.Name != "Rizzoli & Isles" {
		t.Errorf("series name = %q", series.Name)
	}
	for _, baked := range []string{
		workAddr("the-doomsday-key-quot-international-edition-quot"),
		seriesAddr("rizzoli-amp-isles"),
		personAddr("jos-eacute-rollins"),
	} {
		if entryExists(t, dataDir, baked) {
			t.Errorf("a record was minted at %s, a slug spelling the reference", baked)
		}
	}
}

// TestLiteralAmpersandTitleIsUntouched: a title that really spells an
// ampersand keeps it, and slugs exactly as before.
func TestLiteralAmpersandTitleIsUntouched(t *testing.T) {
	_, dataDir := runLibex(t, rows(libexRow{
		asin: "B0AMPLIT01", title: "Tom & Jerry", authors: `{"name":"Ada One"}`,
	}), false)
	var work struct{ Title string }
	readEntity(t, dataDir, workAddr("tom-jerry"), &work)
	if work.Title != "Tom & Jerry" {
		t.Errorf("title = %q, want it unchanged", work.Title)
	}
}

// TestUserLibraryEntityTextImportsDecoded: the decode is runBooks', so a
// user-library source inherits it - here an OpenAudible export whose title,
// joined credit strings and series name arrive escaped.
func TestUserLibraryEntityTextImportsDecoded(t *testing.T) {
	books := `[{"asin":"B0OAENT001","title_short":"Willie &amp; Me","author":"Ana Mar&iacute;a Ruiz",` +
		`"narrated_by":"Leo Kni&#382;ka","series_name":"Pets &amp; Owners","series_sequence":"2",` +
		`"language":"english","region":"US","seconds":36000}]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 {
		t.Fatalf("NewWorks = %d, want 1: %+v", sum.NewWorks, sum)
	}
	var work struct{ Title string }
	readEntity(t, dataDir, workAddr("willie-me"), &work)
	if work.Title != "Willie & Me" {
		t.Errorf("title = %q", work.Title)
	}
	for _, slug := range []string{"ana-maria-ruiz", "leo-knizka"} {
		if !entryExists(t, dataDir, personAddr(slug)) {
			t.Errorf("no person at %q: the escaped credit string was not decoded", slug)
		}
	}
	if !entryExists(t, dataDir, seriesAddr("pets-owners")) {
		t.Error("the escaped series name was not decoded before its slug was minted")
	}
}

// TestEscapedUnnamedCreditIsStillRefused: the unidentifiable-credit refusal
// judges the name as the import will store it. Escaped, a Cyrillic name slugs
// to its code points' digits; decoded, it slugs away to nothing, which is the
// catch-all conflation the refusal exists to prevent.
func TestEscapedUnnamedCreditIsStillRefused(t *testing.T) {
	escaped := "&#1040;&#1085;&#1085;&#1072;" // "Анна"
	if creditIdentifies(escaped) {
		t.Errorf("creditIdentifies(%q) = true; the decoded name identifies nobody", escaped)
	}
	if !creditIdentifies("Erika B&aacute;lint") {
		t.Error("an escaped Latin name must still identify its person")
	}
}

// TestLibexSelectReadsTheDecodedTitle: libex-select composes its books without
// runBooks, so its own door must decode - a selection reading an escaped title
// would key a different work than the import creates.
func TestLibexSelectReadsTheDecodedTitle(t *testing.T) {
	e := rawBook{
		"asin": "B0SELENT01", "title": "Crown &amp; Key", "region": "us",
		"authors":   []any{map[string]any{"name": "Jos&eacute; Rollins"}},
		"narrators": []any{map[string]any{"name": "Ann Reader"}},
		"series":    []any{map[string]any{"name": "Crown &amp; Key", "position": "1"}},
	}
	book := seriesIndex{}.libexBook(e, "B0SELENT01")
	if got := book.str("title_short"); got != "Crown & Key" {
		t.Errorf("title_short = %q", got)
	}
	if len(book.authors) != 1 || book.authors[0] != "José Rollins" {
		t.Errorf("authors = %v", book.authors)
	}
	if len(book.series) != 1 || book.series[0].name != "Crown & Key" {
		t.Errorf("series = %+v", book.series)
	}
}
