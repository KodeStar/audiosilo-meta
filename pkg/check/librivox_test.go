package check

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The LibriVox fields (LICENSING.md's public-domain exception): the schema's
// accept and reject cases, and checkLibrivox's identity rules.

const (
	lvRecRel    = "works/bo/book-one/recordings/rec-one.json"
	lvAuthorRel = "people/au/author-one.json"
	lvNarrRel   = "people/na/narrator-one.json"
)

// librivoxRecording is the base recording carrying every LibriVox field, in
// canonical (sorted-key) form.
func librivoxRecording(id int, chapters string) string {
	return `{"archive_id":"metamorphosis_librivox","chapters":` + chapters +
		`,"id":"rec-one","language":"en","librivox_id":` + strconv.Itoa(id) +
		`,"license":"CC0-1.0","links":{"librivox":"https://librivox.org/the-metamorphosis-by-franz-kafka/","rss":"https://librivox.org/rss/527","zip":"https://archive.org/compress/metamorphosis_librivox/formats=64KBPS MP3&file=/metamorphosis_librivox.zip"},"narrators":["narrator-one"],"sources":[{"imported_at":"2026-10-27","ref":"527","type":"librivox"}],"work":"book-one"}`
}

// withLibrivoxExtras adds the L2 facts (copyright_year, links.text) to a
// librivoxRecording, keeping the canonical key order.
func withLibrivoxExtras(rec string) string {
	rec = strings.Replace(rec, `,"id":"rec-one"`, `,"copyright_year":1915,"id":"rec-one"`, 1)
	return strings.Replace(rec, `"rss":"https://librivox.org/rss/527"`, `"rss":"https://librivox.org/rss/527","text":"https://www.kafka.org/index.php?id=162,170,0,0,1,0"`, 1)
}

const lvChapters = `[{"audio_url":"https://www.archive.org/download/metamorphosis_librivox/metamorphosis_1_kafka_64kb.mp3","length_ms":2992000,"start_ms":0,"title":"I"},{"audio_url":"https://www.archive.org/download/metamorphosis_librivox/metamorphosis_2_kafka_64kb.mp3","length_ms":3231000,"start_ms":2992000,"title":"II"}]`

func TestLibrivoxFieldsValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files[lvRecRel] = withLibrivoxExtras(librivoxRecording(527, lvChapters))
	files[lvAuthorRel] = `{"birth_year":1883,"death_year":1924,"id":"author-one","librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"librivox","ref":"527"}]}`
	// A reader id and an author id are two namespaces: the same number on two
	// people is not a collision.
	files[lvNarrRel] = `{"id":"narrator-one","librivox_reader_id":100,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"librivox"}]}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("LibriVox fields should validate, got:\n%s", joinProblems(res.Problems))
	}
	rec := res.Catalog.Works[0].Recordings[0]
	if rec.LibrivoxID != 527 || rec.ArchiveID != "metamorphosis_librivox" || rec.Links == nil || rec.Links.RSS != "https://librivox.org/rss/527" {
		t.Errorf("recording LibriVox fields did not load: %+v links %+v", rec, rec.Links)
	}
	if rec.CopyrightYear != 1915 || rec.Links.Text != "https://www.kafka.org/index.php?id=162,170,0,0,1,0" {
		t.Errorf("copyright_year / links.text did not load: %d %q", rec.CopyrightYear, rec.Links.Text)
	}
	for _, p := range res.Catalog.People {
		if p.ID == "author-one" && (p.BirthYear != 1883 || p.DeathYear != 1924) {
			t.Errorf("birth/death years did not load: %d %d", p.BirthYear, p.DeathYear)
		}
	}
	if len(rec.Chapters) != 2 || !strings.HasSuffix(rec.Chapters[1].AudioURL, "metamorphosis_2_kafka_64kb.mp3") {
		t.Errorf("chapter audio_url did not load: %+v", rec.Chapters)
	}
	got := map[string][2]int{}
	for _, p := range res.Catalog.People {
		got[p.ID] = [2]int{p.LibrivoxReaderID, p.LibrivoxAuthorID}
	}
	if got["author-one"] != [2]int{0, 100} || got["narrator-one"] != [2]int{100, 0} {
		t.Errorf("person LibriVox ids did not load: %v", got)
	}
}

// lvFidelityChapters numbers its sections from 0 (LibriVox's own numbering,
// project 801) and names a reader per section.
const lvFidelityChapters = `[{"length_ms":2992000,"readers":["narrator-one"],"section_number":0,"start_ms":0,"title":"I"},{"length_ms":3231000,"readers":["author-one","narrator-one"],"section_number":1,"start_ms":2992000,"title":"II"}]`

// withFidelity adds the project's own title, genres and translator credit to a
// librivoxRecording, and a second narrator (author-one reads too).
func withFidelity(rec string) string {
	rec = strings.Replace(rec, `,"id":"rec-one"`, `,"credits":[{"person":"author-one","role":"translator"}],"id":"rec-one"`, 1)
	rec = strings.Replace(rec, `"librivox_id":`, `"librivox_genres":["General Fiction","Humorous Fiction"],"librivox_id":`, 1)
	rec = strings.Replace(rec, `,"license":"CC0-1.0","links"`, `,"librivox_title":"Metamorphosis (version 2)","license":"CC0-1.0","links"`, 1)
	return strings.Replace(rec, `"narrators":["narrator-one"]`, `"narrators":["narrator-one","author-one"]`, 1)
}

// TestLibrivoxAuthorAliasesValid: a sorted alias list beside the record's own
// id, on a plain slug and on an author-id form, each unique across the dataset.
func TestLibrivoxAuthorAliasesValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[200,300],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"librivox","ref":"527"}]}`
	// The same number as an alias, in the other namespace, is no collision.
	files[lvNarrRel] = `{"id":"narrator-one","librivox_reader_aliases":[100,300],"librivox_reader_id":200,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"librivox"}]}`
	files["people/ra/ralph-scott-librivox-author-16428.json"] = `{"id":"ralph-scott-librivox-author-16428","librivox_author_aliases":[16429],"librivox_author_id":16428,"license":"CC0-1.0","name":"Ralph Scott","sources":[{"type":"librivox","ref":"17170"}]}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("author aliases should validate, got:\n%s", joinProblems(res.Problems))
	}
	for _, p := range res.Catalog.People {
		if p.ID == "author-one" && !slices.Equal(p.LibrivoxAuthorAliases, []int{200, 300}) {
			t.Errorf("aliases did not load: %v", p.LibrivoxAuthorAliases)
		}
		if p.ID == "narrator-one" && !slices.Equal(p.LibrivoxReaderAliases, []int{100, 300}) {
			t.Errorf("reader aliases did not load: %v", p.LibrivoxReaderAliases)
		}
	}
}

// TestLibrivoxTombstoneAnswersToItsID: a retired slug of a LibriVox id form points
// at a record answering to that id, as its own or an alias, in its own namespace;
// a retired slug of no LibriVox form asks nothing.
func TestLibrivoxTombstoneAnswersToItsID(t *testing.T) {
	const narrator = `{"id":"narrator-one","librivox_author_aliases":[7],"librivox_author_id":5,"librivox_reader_aliases":[200],"librivox_reader_id":100,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
	for _, tc := range []struct {
		from, want string // want "" is allowed
	}{
		{"narrator-one-librivox-200", ""},                    // a reader alias
		{"narrator-one-librivox-100", ""},                    // the reader's own id
		{"librivox-reader-200", ""},                          // the prefix form
		{"narrator-one-librivox-author-7", ""},               // an author alias
		{"librivox-author-7", ""},                            // the prefix form
		{"an-old-spelling", ""},                              // no LibriVox form
		{"narrator-one-librivox-300", "LibriVox reader 300"}, // no such id
		{"librivox-reader-300", "LibriVox reader 300"},
		{"narrator-one-librivox-author-300", "LibriVox author 300"},
		{"librivox-author-300", "LibriVox author 300"},
		{"narrator-one-librivox-7", "LibriVox reader 7"},            // an author alias, not a reader's
		{"narrator-one-librivox-author-200", "LibriVox author 200"}, // a reader alias, not an author's
	} {
		t.Run(tc.from, func(t *testing.T) {
			dir := t.TempDir()
			files := baseValid()
			files[lvNarrRel] = narrator
			files["redirects.json"] = `{"people":{"` + tc.from + `":"narrator-one"},"series":{},"works":{}}`
			writeEntities(t, dir, files)
			res := Load(dir)
			if tc.want == "" {
				if !res.OK() {
					t.Fatalf("should validate, got:\n%s", joinProblems(res.Problems))
				}
				return
			}
			if !strings.Contains(joinProblems(res.Problems), tc.want) {
				t.Errorf("no problem contained %q; problems:\n%s", tc.want, joinProblems(res.Problems))
			}
		})
	}
}

func TestLibrivoxFidelityFieldsValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files[lvRecRel] = withFidelity(librivoxRecording(801, lvFidelityChapters))
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("LibriVox fidelity fields should validate, got:\n%s", joinProblems(res.Problems))
	}
	rec := res.Catalog.Works[0].Recordings[0]
	if rec.LibrivoxTitle != "Metamorphosis (version 2)" || len(rec.LibrivoxGenres) != 2 || len(rec.Credits) != 1 || rec.Credits[0].Person != "author-one" {
		t.Errorf("recording fidelity fields did not load: %+v", rec)
	}
	if len(rec.Chapters) != 2 || rec.Chapters[0].SectionNumber == nil || *rec.Chapters[0].SectionNumber != 0 ||
		rec.Chapters[1].SectionNumber == nil || *rec.Chapters[1].SectionNumber != 1 || len(rec.Chapters[1].Readers) != 2 {
		t.Errorf("chapter readers / section numbers did not load: %+v", rec.Chapters)
	}
}

func TestLibrivoxRuleViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{
			name: "librivox_id is an integer",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(librivoxRecording(527, lvChapters), `"librivox_id":527`, `"librivox_id":"527"`, 1)
			},
			want: "librivox_id",
		},
		{
			name: "librivox_id is positive",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(librivoxRecording(527, lvChapters), `"librivox_id":527`, `"librivox_id":0`, 1)
			},
			want: "librivox_id",
		},
		{
			name: "a link must be https",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(librivoxRecording(527, lvChapters), `"rss":"https://librivox.org/rss/527"`, `"rss":"http://librivox.org/rss/527"`, 1)
			},
			want: "does not match pattern",
		},
		{
			name: "links names only librivox, zip, rss and text",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(librivoxRecording(527, lvChapters), `"links":{`, `"links":{"forum":"https://forum.librivox.org/x",`, 1)
			},
			want: "additional properties 'forum' not allowed",
		},
		{
			name: "links is never empty",
			mutate: func(f map[string]string) {
				s := librivoxRecording(527, lvChapters)
				start := strings.Index(s, `"links":{`)
				end := strings.Index(s[start:], `},`) + start + 2
				f[lvRecRel] = s[:start] + `"links":{},` + s[end:]
			},
			want: "minProperties",
		},
		{
			name: "a chapter audio_url must be https",
			mutate: func(f map[string]string) {
				f[lvRecRel] = librivoxRecording(527, strings.Replace(lvChapters, `"audio_url":"https://www.archive.org`, `"audio_url":"ftp://www.archive.org`, 1))
			},
			want: "does not match pattern",
		},
		{
			name: "archive_id is an archive.org identifier",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(librivoxRecording(527, lvChapters), `"archive_id":"metamorphosis_librivox"`, `"archive_id":"metamorphosis librivox/x"`, 1)
			},
			want: "archive_id",
		},
		{
			name: "person librivox_reader_id is an integer",
			mutate: func(f map[string]string) {
				f[lvNarrRel] = `{"id":"narrator-one","librivox_reader_id":"94","license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: "librivox_reader_id",
		},
		{
			name: "a source type outside the vocabulary",
			mutate: func(f map[string]string) {
				f[lvNarrRel] = `{"id":"narrator-one","license":"CC0-1.0","name":"Narrator One","sources":[{"type":"librivox-import"}]}`
			},
			want: "sources",
		},
		{
			name: "copyright_year is an integer",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withLibrivoxExtras(librivoxRecording(527, lvChapters)), `"copyright_year":1915`, `"copyright_year":"1915"`, 1)
			},
			want: "copyright_year",
		},
		{
			name: "copyright_year is never 0",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withLibrivoxExtras(librivoxRecording(527, lvChapters)), `"copyright_year":1915`, `"copyright_year":0`, 1)
			},
			want: "copyright_year",
		},
		{
			name: "links.text must be https",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withLibrivoxExtras(librivoxRecording(527, lvChapters)), `"text":"https://www.kafka.org`, `"text":"http://www.kafka.org`, 1)
			},
			want: "does not match pattern",
		},
		{
			name: "birth_year is never 0",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"birth_year":0,"id":"author-one","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "birth_year",
		},
		{
			name: "death_year is a year, not a word",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"death_year":"alive","id":"author-one","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "death_year",
		},
		{
			name: "death_year before birth_year",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"birth_year":1924,"death_year":1883,"id":"author-one","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "death_year 1883 is before birth_year 1924",
		},
		{
			name: "audio_url without librivox_id",
			mutate: func(f map[string]string) {
				f[lvRecRel] = withChapters(lvChapters)
			},
			want: "has an audio_url but the recording has no librivox_id",
		},
		{
			name: "librivox_title without librivox_id",
			mutate: func(f map[string]string) {
				f[lvRecRel] = `{"abridged":false,"id":"rec-one","language":"en","librivox_title":"Book One (version 2)","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
			},
			want: "librivox_title without a librivox_id",
		},
		{
			name: "librivox_genres without librivox_id",
			mutate: func(f map[string]string) {
				f[lvRecRel] = `{"abridged":false,"id":"rec-one","language":"en","librivox_genres":["Poetry"],"license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
			},
			want: "librivox_genres without a librivox_id",
		},
		{
			name: "librivox_genres repeats a genre",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withFidelity(librivoxRecording(801, lvFidelityChapters)), `["General Fiction","Humorous Fiction"]`, `["Poetry","Poetry"]`, 1)
			},
			want: "librivox_genres",
		},
		{
			name: "recording credits without librivox_id",
			mutate: func(f map[string]string) {
				f[lvRecRel] = `{"abridged":false,"credits":[{"person":"author-one","role":"translator"}],"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
			},
			want: "credits without a librivox_id",
		},
		{
			name: "chapter readers without librivox_id",
			mutate: func(f map[string]string) {
				f[lvRecRel] = withChapters(`[{"length_ms":1000,"readers":["narrator-one"],"start_ms":0,"title":"I"},{"length_ms":1000,"start_ms":1000,"title":"II"}]`)
			},
			want: "chapter 0 has readers but the recording has no librivox_id",
		},
		{
			name: "section_number without librivox_id",
			mutate: func(f map[string]string) {
				f[lvRecRel] = withChapters(`[{"length_ms":1000,"section_number":0,"start_ms":0,"title":"I"}]`)
			},
			want: "has a section_number but the recording has no librivox_id",
		},
		{
			name: "section_number equal to its position",
			mutate: func(f map[string]string) {
				f[lvRecRel] = withFidelity(librivoxRecording(801, strings.Replace(lvFidelityChapters, `"section_number":1,`, `"section_number":2,`, 1)))
			},
			want: "chapter 1 states section_number 2, its own position",
		},
		{
			name: "section_number is never negative",
			mutate: func(f map[string]string) {
				f[lvRecRel] = withFidelity(librivoxRecording(801, strings.Replace(lvFidelityChapters, `"section_number":0,`, `"section_number":-1,`, 1)))
			},
			want: "section_number",
		},
		{
			name: "a chapter reader who is not a narrator",
			mutate: func(f map[string]string) {
				f[lvRecRel] = librivoxRecording(801, lvFidelityChapters)
			},
			want: `chapter 1 reader "author-one" is not one of the recording's narrators`,
		},
		{
			name: "readers on every chapter of a one-narrator recording",
			mutate: func(f map[string]string) {
				f[lvRecRel] = librivoxRecording(801, strings.Replace(lvFidelityChapters, `["author-one","narrator-one"]`, `["narrator-one"]`, 1))
			},
			want: "every chapter lists readers on a recording with one narrator",
		},
		{
			name: "a chapter reader who does not exist",
			mutate: func(f map[string]string) {
				f[lvRecRel] = withFidelity(librivoxRecording(801, strings.Replace(lvFidelityChapters, `["narrator-one"]`, `["nobody-here"]`, 1)))
			},
			want: `chapter reader "nobody-here" does not exist as a person`,
		},
		{
			name: "a recording credit who does not exist",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withFidelity(librivoxRecording(801, lvFidelityChapters)), `"person":"author-one"`, `"person":"nobody-here"`, 1)
			},
			want: `credit "nobody-here" (translator) does not exist as a person`,
		},
		{
			name: "a recording credit listed twice",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withFidelity(librivoxRecording(801, lvFidelityChapters)), `"credits":[{"person":"author-one","role":"translator"}]`,
					`"credits":[{"person":"author-one","role":"translator"},{"person":"author-one","role":"translator"}]`, 1)
			},
			want: `credit "author-one" is listed twice as "translator"`,
		},
		{
			name: "a recording credit role outside the vocabulary",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withFidelity(librivoxRecording(801, lvFidelityChapters)), `"role":"translator"`, `"role":"narrator"`, 1)
			},
			want: "credits",
		},
		{
			// editor is a WORK credit role; a recording states only its translator.
			name: "a recording credit in a work-only role",
			mutate: func(f map[string]string) {
				f[lvRecRel] = strings.Replace(withFidelity(librivoxRecording(801, lvFidelityChapters)), `"role":"translator"`, `"role":"editor"`, 1)
			},
			want: "credits",
		},
		{
			name: "two recordings claim one librivox_id",
			mutate: func(f map[string]string) {
				f[lvRecRel] = librivoxRecording(527, lvChapters)
				f["works/bo/book-one/recordings/rec-two.json"] = strings.Replace(librivoxRecording(527, lvChapters), `"id":"rec-one"`, `"id":"rec-two"`, 1)
			},
			want: "duplicate librivox_id 527",
		},
		{
			name: "two people claim one librivox_reader_id",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_reader_id":94,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
				f[lvNarrRel] = `{"id":"narrator-one","librivox_reader_id":94,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: "duplicate librivox_reader_id 94",
		},
		{
			name: "two people claim one librivox_author_id",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
				f[lvNarrRel] = `{"id":"narrator-one","librivox_author_id":100,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: "duplicate librivox_author_id 100",
		},
		{
			name: "an alias needs a librivox_author_id beside it",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[200],"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "librivox_author_id",
		},
		{
			name: "an alias list is not empty",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "librivox_author_aliases",
		},
		{
			name: "an alias list repeats no id",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[200,200],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "librivox_author_aliases",
		},
		{
			name: "an alias list is sorted",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[300,200],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "not sorted",
		},
		{
			name: "an alias is not the record's own id",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[100],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "librivox_author_aliases names 100",
		},
		{
			name: "an alias is not another person's id",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[200],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
				f[lvNarrRel] = `{"id":"narrator-one","librivox_author_id":200,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: "librivox_author_aliases names 200",
		},
		{
			name: "an alias is not another person's alias",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_author_aliases":[300],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
				f[lvNarrRel] = `{"id":"narrator-one","librivox_author_aliases":[300],"librivox_author_id":200,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: "librivox_author_aliases names 300",
		},
		{
			name: "a reader alias needs a librivox_reader_id beside it",
			mutate: func(f map[string]string) {
				f[lvNarrRel] = `{"id":"narrator-one","librivox_reader_aliases":[200],"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: "librivox_reader_id",
		},
		{
			name: "a reader alias is not another person's reader id",
			mutate: func(f map[string]string) {
				f[lvAuthorRel] = `{"id":"author-one","librivox_reader_id":200,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
				f[lvNarrRel] = `{"id":"narrator-one","librivox_reader_aliases":[200],"librivox_reader_id":100,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: "librivox_reader_aliases names 200, already a LibriVox reader id",
		},
		{
			name: "a slug composed from a reader alias is not the record's id",
			mutate: func(f map[string]string) {
				f["people/na/narrator-one-librivox-200.json"] = `{"id":"narrator-one-librivox-200","librivox_reader_aliases":[200],"librivox_reader_id":100,"license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`
			},
			want: `person id "narrator-one-librivox-200" does not match`,
		},
		{
			name: "a slug composed from an alias is not the record's id",
			mutate: func(f map[string]string) {
				f["people/au/author-one-librivox-author-200.json"] = `{"id":"author-one-librivox-author-200","librivox_author_aliases":[200],"librivox_author_id":100,"license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: `person id "author-one-librivox-author-200" does not match`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := baseValid()
			c.mutate(files)
			writeEntities(t, dir, files)
			res := Load(dir)
			if res.OK() {
				t.Fatalf("expected a problem containing %q, got none", c.want)
			}
			for _, p := range res.Problems {
				if strings.Contains(p.Msg, c.want) {
					return
				}
			}
			t.Errorf("no problem contained %q; problems:\n%s", c.want, joinProblems(res.Problems))
		})
	}
}

// TestLifeYearsBC: negative years are years BC, so a death in -348 after a
// birth in -428 is in order.
func TestLifeYearsBC(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files[lvAuthorRel] = `{"birth_year":-428,"death_year":-348,"id":"author-one","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
	writeEntities(t, dir, files)
	if res := Load(dir); !res.OK() {
		t.Fatalf("BC life years should validate, got:\n%s", joinProblems(res.Problems))
	}
}

// TestLibrivoxOneNarratorWithAnUnreadSection: a one-narrator recording may list
// its narrator on some chapters when another chapter states no reader - that
// is not the omitted spelling, it says the unread section states none.
func TestLibrivoxOneNarratorWithAnUnreadSection(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files[lvRecRel] = librivoxRecording(801, `[{"length_ms":2992000,"readers":["narrator-one"],"section_number":0,"start_ms":0,"title":"I"},{"length_ms":3231000,"section_number":1,"start_ms":2992000,"title":"II"}]`)
	writeEntities(t, dir, files)
	if res := Load(dir); !res.OK() {
		t.Fatalf("should validate, got:\n%s", joinProblems(res.Problems))
	}
}
