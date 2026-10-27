package check

import (
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
