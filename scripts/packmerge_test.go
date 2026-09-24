// Package scripts_test verifies the operator scripts that CI and contributors
// run by hand. They are shell, so they are exercised the way they are used - by
// driving a real git conflict and running the script on it - rather than by
// re-implementing their logic in Go.
package scripts_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The three-way merge behind the pack-conflict recipe. Every case here is a real
// rebase: a base commit, a commit on main, a commit on the branch, and the
// script run on the conflict git leaves behind.
//
// The rules it has to get right are all about DELETION. A plain union of the two
// sides cannot express one - the side that removed a record gets it handed back
// by the side that still has it, silently - and this repository deletes records
// (a retracted source, a work that turned out to be a duplicate).

func work(id, title string) string {
	return `"` + id + `":{"id":"` + id + `","title":"` + title + `"}`
}

func workWithRecs(id, title, recs string) string {
	return `"` + id + `":{"id":"` + id + `","title":"` + title + `","recordings":{` + recs + `}}`
}

func rec(id, publisher string) string {
	if publisher == "" {
		return `"` + id + `":{"id":"` + id + `","work":"aaa"}`
	}
	return `"` + id + `":{"id":"` + id + `","work":"aaa","publisher":"` + publisher + `"}`
}

func pack(entries ...string) string {
	return `{"entries":{` + strings.Join(entries, ",") + `}}` + "\n"
}

// A works-community entry has no own fields: it is the map of its members, one
// per sidecar, each a licensed document of its own.
func community(id string, members ...string) string {
	return `"` + id + `":{` + strings.Join(members, ",") + `}`
}

func charsMember(work, name string) string {
	return `"characters":{"work":"` + work + `","license":"CC-BY-SA-4.0",` +
		`"characters":[{"id":"c1","name":"` + name + `"}]}`
}

func recapsMember(work, text string) string {
	return `"recaps":{"work":"` + work + `","license":"CC-BY-SA-4.0",` +
		`"recaps":[{"through":{"chapter":1},"text":"` + text + `"}]}`
}

func descriptionMember(work, text string) string {
	return `"description":{"work":"` + work + `","license":"CC-BY-SA-4.0","text":"` + text + `"}`
}

// entry renders one pack entry from field fragments. The both-sides-added cases
// below differ in one field apiece, which is easier to read written out than
// threaded through a helper per shape.
func entry(id string, fields ...string) string {
	return `"` + id + `":{"id":"` + id + `","license":"CC0-1.0",` + strings.Join(fields, ",") + `}`
}

// source is one provenance stamp. Two imports of overlapping libraries mint the
// same record from whichever ASIN each saw first, so this is what differs
// between two copies of a record nobody disagrees about.
func source(typ, ref, at string) string {
	return `{"imported_at":"` + at + `","ref":"` + ref + `","type":"` + typ + `"}`
}

func sources(list ...string) string {
	return `"sources":[` + strings.Join(list, ",") + `]`
}

// seriesWork is one (work, position) membership in a series entry.
func seriesWork(work, position string) string {
	return `{"position":"` + position + `","work":"` + work + `"}`
}

func seriesWorks(list ...string) string {
	return `"works":[` + strings.Join(list, ",") + `]`
}

// The four families, since the script reads the family off the path and only the
// series works rule is not family-neutral.
const (
	worksPath     = "data/works/0/0.json"
	communityPath = "data/works-community/0/0.json"
	peoplePath    = "data/people/0.json"
	seriesPath    = "data/series/0.json"
	// The tree's one NON-pack file, which this script must refuse rather than
	// merge (see TestPackMergeRefusesANonPackFile).
	redirectsPath = "data/redirects.json"
)

func TestPackUnionMerge(t *testing.T) {
	requireTools(t, "git", "jq")
	script := abs(t, "pack-union-merge.sh")

	cases := []struct {
		name string
		// path is the pack file, which names the FAMILY and so decides which
		// one-level-deeper exception the script may apply. Empty means works.
		path string
		// base, main and branch are the three versions of the pack file.
		base, main, branch string
		// wantRefusal expects exit 5: a conflict only a person can settle.
		wantRefusal bool
		// present and absent are entry paths (jq-style, "aaa" or
		// "aaa.recordings.r1") the merged file must and must not hold.
		present, absent []string
		// equal maps such a path to the JSON the merged file must hold there,
		// for the cases where WHAT was merged is the point: a unioned sources
		// list, a superset series, an author order that was kept.
		equal map[string]string
	}{
		{
			name:    "each side adds its own record",
			base:    pack(work("aaa", "A")),
			main:    pack(work("aaa", "A"), work("mmm", "M")),
			branch:  pack(work("aaa", "A"), work("zzz", "Z")),
			present: []string{"aaa", "mmm", "zzz"},
		},
		{
			// The bug a two-sided union has: the branch deleted ddd, main still
			// has it, and a union would hand it straight back.
			name:    "a record the branch deleted stays deleted",
			base:    pack(work("aaa", "A"), work("ddd", "D")),
			main:    pack(work("aaa", "A"), work("ddd", "D"), work("mmm", "M")),
			branch:  pack(work("aaa", "A")),
			present: []string{"aaa", "mmm"},
			absent:  []string{"ddd"},
		},
		{
			name:    "a record main deleted stays deleted",
			base:    pack(work("aaa", "A"), work("ddd", "D")),
			main:    pack(work("aaa", "A")),
			branch:  pack(work("aaa", "A"), work("ddd", "D"), work("zzz", "Z")),
			present: []string{"aaa", "zzz"},
			absent:  []string{"ddd"},
		},
		{
			name:    "a record both sides deleted stays deleted",
			base:    pack(work("aaa", "A"), work("ddd", "D")),
			main:    pack(work("aaa", "A"), work("mmm", "M")),
			branch:  pack(work("aaa", "A"), work("zzz", "Z")),
			present: []string{"mmm", "zzz"},
			absent:  []string{"ddd"},
		},
		{
			name:        "one side deleted what the other edited",
			base:        pack(work("aaa", "A"), work("ddd", "D")),
			main:        pack(work("aaa", "A"), work("ddd", "D edited")),
			branch:      pack(work("aaa", "A")),
			wantRefusal: true,
		},
		{
			name:        "both sides edited one record",
			base:        pack(work("aaa", "A")),
			main:        pack(work("aaa", "A from main")),
			branch:      pack(work("aaa", "A from the branch")),
			wantRefusal: true,
		},
		{
			// The common intake collision: two narrations of one book.
			name:    "each side adds a recording to one work",
			base:    pack(workWithRecs("aaa", "A", rec("r1", ""))),
			main:    pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", ""))),
			branch:  pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r3", ""))),
			present: []string{"aaa.recordings.r1", "aaa.recordings.r2", "aaa.recordings.r3"},
		},
		{
			name:    "a recording the branch deleted stays deleted",
			base:    pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", ""))),
			main:    pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", "")+","+rec("r9", ""))),
			branch:  pack(workWithRecs("aaa", "A", rec("r1", ""))),
			present: []string{"aaa.recordings.r1", "aaa.recordings.r9"},
			absent:  []string{"aaa.recordings.r2"},
		},
		{
			name:        "both sides edited one recording",
			base:        pack(workWithRecs("aaa", "A", rec("r1", "old"))),
			main:        pack(workWithRecs("aaa", "A", rec("r1", "main"))),
			branch:      pack(workWithRecs("aaa", "A", rec("r1", "branch"))),
			wantRefusal: true,
		},
		{
			// Only the recordings map may differ: a work's own fields differing
			// is two people disagreeing about the book.
			name:        "the work's own fields differ too",
			base:        pack(workWithRecs("aaa", "A", rec("r1", ""))),
			main:        pack(workWithRecs("aaa", "A from main", rec("r1", "")+","+rec("r2", ""))),
			branch:      pack(workWithRecs("aaa", "A from the branch", rec("r1", "")+","+rec("r3", ""))),
			wantRefusal: true,
		},
		{
			// The works-community twin of the narrations collision, and the one
			// the live intake stream hits: a characters pull request and a recaps
			// pull request for the same book each CREATE the same entry, carrying
			// one disjoint member apiece.
			name:   "each side adds its own community member for one work",
			path:   communityPath,
			base:   pack(community("aaa", charsMember("aaa", "Ann"))),
			main:   pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", charsMember("bbb", "Bob"))),
			branch: pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", recapsMember("bbb", "So far"))),
			present: []string{
				"aaa.characters", "bbb.characters", "bbb.recaps",
			},
		},
		{
			// The member rule is over the entry's KEYS, not a list of the kinds
			// that existed when it was written: a description arriving beside a
			// recap merges on the same terms its siblings do.
			name:   "a description member merges beside a sibling",
			path:   communityPath,
			base:   pack(community("aaa", charsMember("aaa", "Ann"))),
			main:   pack(community("aaa", charsMember("aaa", "Ann")+","+descriptionMember("aaa", "A book about a thing."))),
			branch: pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			present: []string{
				"aaa.characters", "aaa.description", "aaa.recaps",
			},
		},
		{
			// And two people writing the description of one book is the same
			// disagreement two people writing its characters is.
			name:        "both sides wrote the description member",
			path:        communityPath,
			base:        pack(community("aaa", charsMember("aaa", "Ann"))),
			main:        pack(community("aaa", charsMember("aaa", "Ann")+","+descriptionMember("aaa", "One account."))),
			branch:      pack(community("aaa", charsMember("aaa", "Ann")+","+descriptionMember("aaa", "Another account."))),
			wantRefusal: true,
		},
		{
			// Two people describing the same characters is a disagreement about
			// one text, exactly as two people editing one recording is.
			name:        "both sides wrote the same community member",
			path:        communityPath,
			base:        pack(community("aaa", charsMember("aaa", "Ann"))),
			main:        pack(community("aaa", charsMember("aaa", "Ann from main"))),
			branch:      pack(community("aaa", charsMember("aaa", "Ann from the branch"))),
			wantRefusal: true,
		},
		{
			// The base decides per MEMBER: the branch removed the recaps, main
			// left them exactly as the base had them, so they are deleted rather
			// than handed back.
			name:    "a community member the branch deleted stays deleted",
			path:    communityPath,
			base:    pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			main:    pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far")), community("mmm", charsMember("mmm", "Em"))),
			branch:  pack(community("aaa", charsMember("aaa", "Ann"))),
			present: []string{"aaa.characters", "mmm.characters"},
			absent:  []string{"aaa.recaps"},
		},
		{
			// Both deletions are honoured, and an entry with no members left is
			// nothing - it goes rather than staying behind as an empty record.
			name:    "both sides deleted a different community member",
			path:    communityPath,
			base:    pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			main:    pack(community("aaa", charsMember("aaa", "Ann")), community("mmm", charsMember("mmm", "Em"))),
			branch:  pack(community("aaa", recapsMember("aaa", "So far"))),
			present: []string{"mmm.characters"},
			absent:  []string{"aaa"},
		},
		{
			// Delete versus modify at the member level is a person's call, the
			// same way it is at the entry level.
			name:        "one side deleted the community member the other rewrote",
			path:        communityPath,
			base:        pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			main:        pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far, rewritten"))),
			branch:      pack(community("aaa", charsMember("aaa", "Ann"))),
			wantRefusal: true,
		},
		{
			// A family with no exception gets none: a person entry both sides
			// rewrote is a refusal whatever its shape.
			name:        "both sides edited one person record",
			path:        peoplePath,
			base:        pack(work("aaa", "A")),
			main:        pack(work("aaa", "A from main")),
			branch:      pack(work("aaa", "A from the branch")),
			wantRefusal: true,
		},

		// An entry present on both sides that ONE side left exactly as the base
		// had it was changed by the other side only, so the changed version
		// stands in every family - the ordinary three-way rule, which the
		// refusal above used to pre-empt. The live shape (#2338): the daily sync
		// bot appended volumes to one series on main while an intake branch
		// added two other series to the same pack, and every rebase of that
		// branch was refused as "both sides changed" the series only main had.
		{
			name: "main changed one series entry, the branch added another",
			path: seriesPath,
			base: pack(entry("ser", `"name":"S"`, seriesWorks(seriesWork("w1", "1")))),
			main: pack(entry("ser", `"name":"S"`,
				seriesWorks(seriesWork("w1", "1"), seriesWork("w2", "2"), seriesWork("w3", "3")))),
			branch: pack(entry("ser", `"name":"S"`, seriesWorks(seriesWork("w1", "1"))),
				entry("tan", `"name":"T"`, seriesWorks(seriesWork("w9", "1")))),
			present: []string{"tan"},
			equal: map[string]string{
				"ser.works": `[` + seriesWork("w1", "1") + `,` + seriesWork("w2", "2") + `,` +
					seriesWork("w3", "3") + `]`,
			},
		},
		{
			name: "the branch changed one series entry, main added another",
			path: seriesPath,
			base: pack(entry("ser", `"name":"S"`, seriesWorks(seriesWork("w1", "1")))),
			main: pack(entry("ser", `"name":"S"`, seriesWorks(seriesWork("w1", "1"))),
				entry("tan", `"name":"T"`, seriesWorks(seriesWork("w9", "1")))),
			branch: pack(entry("ser", `"name":"S"`,
				seriesWorks(seriesWork("w1", "1"), seriesWork("w2", "2")))),
			present: []string{"tan"},
			equal: map[string]string{
				"ser.works": `[` + seriesWork("w1", "1") + `,` + seriesWork("w2", "2") + `]`,
			},
		},
		{
			// Both sides changing it is still two accounts of one series.
			name: "both sides changed one series entry",
			path: seriesPath,
			base: pack(entry("ser", `"name":"S"`, seriesWorks(seriesWork("w1", "1")))),
			main: pack(entry("ser", `"name":"S"`,
				seriesWorks(seriesWork("w1", "1"), seriesWork("w2", "2")))),
			branch: pack(entry("ser", `"name":"S"`,
				seriesWorks(seriesWork("w1", "1"), seriesWork("w3", "3")))),
			wantRefusal: true,
		},
		{
			name:    "main changed one person record, the branch added another",
			path:    peoplePath,
			base:    pack(entry("aaa", `"name":"A"`)),
			main:    pack(entry("aaa", `"name":"A"`, `"kind":"group"`)),
			branch:  pack(entry("aaa", `"name":"A"`), entry("bob", `"name":"Bob"`)),
			present: []string{"bob"},
			equal:   map[string]string{"aaa.kind": `"group"`},
		},
		{
			name:    "the branch changed one person record, main added another",
			path:    peoplePath,
			base:    pack(entry("aaa", `"name":"A"`)),
			main:    pack(entry("aaa", `"name":"A"`), entry("bob", `"name":"Bob"`)),
			branch:  pack(entry("aaa", `"name":"A"`, `"kind":"group"`)),
			present: []string{"bob"},
			equal:   map[string]string{"aaa.kind": `"group"`},
		},
		{
			// The same rule one level down, where the family exceptions merge:
			// a work's own fields as one unit, a recording, a community member.
			name:   "main changed the work's own fields, the branch added a recording",
			base:   pack(workWithRecs("aaa", "A", rec("r1", ""))),
			main:   pack(workWithRecs("aaa", "A corrected", rec("r1", ""))),
			branch: pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", ""))),
			present: []string{
				"aaa.recordings.r1", "aaa.recordings.r2",
			},
			equal: map[string]string{"aaa.title": `"A corrected"`},
		},
		{
			name:    "the branch changed a recording, main added another",
			base:    pack(workWithRecs("aaa", "A", rec("r1", ""))),
			main:    pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", ""))),
			branch:  pack(workWithRecs("aaa", "A", rec("r1", "Imprint"))),
			present: []string{"aaa.recordings.r2"},
			equal:   map[string]string{"aaa.recordings.r1.publisher": `"Imprint"`},
		},
		{
			name:   "main rewrote a community member, the branch added another",
			path:   communityPath,
			base:   pack(community("aaa", charsMember("aaa", "Ann"))),
			main:   pack(community("aaa", charsMember("aaa", "Ann, rewritten"))),
			branch: pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			present: []string{
				"aaa.recaps",
			},
			equal: map[string]string{"aaa.characters.characters": `[{"id":"c1","name":"Ann, rewritten"}]`},
		},

		// Everything below is one entry BOTH SIDES ADDED, which the base cannot
		// call an edit conflict because it has no version to have been edited
		// from. These are the shapes one contributor produced by submitting two
		// overlapping library exports (issues #2274/#2275, 142 of the second's
		// 150 rows already in the first): after the first import merged, the
		// second was refused in 29 packs, every one of them two copies of one
		// record that did not contradict each other.
		{
			// The commonest of the 29: the importer mints a person from the
			// first ASIN it saw them on, so two runs stamp a different ref.
			// Provenance accumulates; it is not a claim about the person.
			name:   "both sides minted one person from a different source",
			path:   peoplePath,
			base:   pack(entry("aaa", `"name":"A"`, sources(source("user", "", "2026-01-01")))),
			main:   pack(entry("aaa", `"name":"A"`, sources(source("user", "", "2026-01-01"))), entry("bob", `"name":"Bob"`, sources(source("libex-import", "B000000001", "2026-09-01")))),
			branch: pack(entry("aaa", `"name":"A"`, sources(source("user", "", "2026-01-01"))), entry("bob", `"name":"Bob"`, sources(source("libex-import", "B000000002", "2026-09-02")))),
			equal: map[string]string{
				// Ours first - in a rebase that is what is already on main.
				"bob.sources": `[` + source("libex-import", "B000000001", "2026-09-01") + `,` +
					source("libex-import", "B000000002", "2026-09-02") + `]`,
			},
		},
		{
			// And the one that is a real disagreement: two people naming the
			// same slug differently is a fact to settle, not a field to union.
			name:        "both sides added one person under a different name",
			path:        peoplePath,
			base:        pack(entry("aaa", `"name":"A"`)),
			main:        pack(entry("aaa", `"name":"A"`), entry("bob", `"name":"Bob Smith"`)),
			branch:      pack(entry("aaa", `"name":"A"`), entry("bob", `"name":"Robert Smith"`)),
			wantRefusal: true,
		},
		{
			// A series the second export saw more of: the same (work, position)
			// pairs plus more, and its own minting source.
			name: "both sides added one series, one seeing more of it",
			path: seriesPath,
			base: pack(entry("aaa", `"name":"A"`)),
			main: pack(entry("aaa", `"name":"A"`), entry("ser", `"name":"S"`,
				seriesWorks(seriesWork("w1", "1"), seriesWork("w2", "2")),
				sources(source("libex-import", "B000000001", "2026-09-01")))),
			branch: pack(entry("aaa", `"name":"A"`), entry("ser", `"name":"S"`,
				seriesWorks(seriesWork("w1", "1"), seriesWork("w2", "2"), seriesWork("w3", "3")),
				sources(source("libex-import", "B000000002", "2026-09-02")))),
			equal: map[string]string{
				"ser.works": `[` + seriesWork("w1", "1") + `,` + seriesWork("w2", "2") + `,` +
					seriesWork("w3", "3") + `]`,
				"ser.sources": `[` + source("libex-import", "B000000001", "2026-09-01") + `,` +
					source("libex-import", "B000000002", "2026-09-02") + `]`,
			},
		},
		{
			// The series list is unioned by the PAIR, so one position naming two
			// works is a disagreement about the series and stays one.
			name: "the two series put a different work at one position",
			path: seriesPath,
			base: pack(entry("aaa", `"name":"A"`)),
			main: pack(entry("aaa", `"name":"A"`),
				entry("ser", `"name":"S"`, seriesWorks(seriesWork("w1", "1")))),
			branch: pack(entry("aaa", `"name":"A"`),
				entry("ser", `"name":"S"`, seriesWorks(seriesWork("w9", "1")))),
			wantRefusal: true,
		},
		{
			// Five of the 29: the same authors, listed the other way round.
			// Order is the source's billing statement, so one of them is kept
			// rather than the list being sorted into something neither wrote.
			name: "both sides added one work with the authors in another order",
			base: pack(entry("aaa", `"title":"A"`)),
			main: pack(entry("aaa", `"title":"A"`),
				entry("www", `"title":"W"`, `"authors":["ann-lee","bo-park"]`)),
			branch: pack(entry("aaa", `"title":"A"`),
				entry("www", `"title":"W"`, `"authors":["bo-park","ann-lee"]`)),
			equal: map[string]string{`www.authors`: `["ann-lee","bo-park"]`},
		},
		{
			// Twenty-one of the 29: the second run had a libex fill behind it,
			// so its copy carries keys the first never had - genres on the work,
			// chapters and a cover on the recording - and an extra import stamp
			// on both. Every key BOTH sides have is equal, so nothing is in
			// dispute and the fuller copy is simply taken where it is fuller.
			name: "one side filled in the work and its recording",
			base: pack(entry("aaa", `"title":"A"`)),
			main: pack(entry("aaa", `"title":"A"`), entry("www",
				`"title":"W"`, `"authors":["ann-lee"]`,
				sources(source("user", "", "2026-09-01")),
				`"recordings":{"r1":{"id":"r1","work":"www","narrators":["kit-doe","lou-ray"],`+
					sources(source("user", "", "2026-09-01"))+`}}`)),
			branch: pack(entry("aaa", `"title":"A"`), entry("www",
				`"title":"W"`, `"authors":["ann-lee"]`, `"genres":["epic-fantasy"]`,
				sources(source("user", "", "2026-09-01"), source("libex-import", "B000000001", "2026-09-02")),
				`"recordings":{"r1":{"id":"r1","work":"www","narrators":["lou-ray","kit-doe"],`+
					`"cover_url":"https://example.invalid/c.jpg","chapters":[{"title":"One","start":0}],`+
					sources(source("user", "", "2026-09-01"), source("libex-import", "B000000001", "2026-09-02"))+`}}`)),
			present: []string{
				"www.genres", "www.recordings.r1.chapters", "www.recordings.r1.cover_url",
			},
			equal: map[string]string{
				"www.sources": `[` + source("user", "", "2026-09-01") + `,` +
					source("libex-import", "B000000001", "2026-09-02") + `]`,
				// Same set, other order, one level down inside the recording.
				"www.recordings.r1.narrators": `["kit-doe","lou-ray"]`,
			},
		},
		{
			// The rule is family-neutral: a community entry both sides created
			// merges on the same terms, member by member.
			name:   "both sides added one community entry with the same member",
			path:   communityPath,
			base:   pack(community("aaa", charsMember("aaa", "Ann"))),
			main:   pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", charsMember("bbb", "Bob"))),
			branch: pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", charsMember("bbb", "Bob")+","+recapsMember("bbb", "So far"))),
			present: []string{
				"bbb.characters", "bbb.recaps",
			},
		},
		{
			// ...and the same member with a different text in it is still two
			// people disagreeing about one document.
			name:        "both sides added one community entry with a contradicting member",
			path:        communityPath,
			base:        pack(community("aaa", charsMember("aaa", "Ann"))),
			main:        pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", charsMember("bbb", "Bob"))),
			branch:      pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", charsMember("bbb", "Robert"))),
			wantRefusal: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packPath := c.path
			if packPath == "" {
				packPath = worksPath
			}
			repo := conflictedRebase(t, packPath, c.base, c.main, c.branch)

			out, err := runIn(repo, script, packPath)
			if c.wantRefusal {
				if err == nil {
					t.Fatalf("the merge went ahead; it must refuse this one:\n%s", out)
				}
				if code := exitCode(err); code != 5 {
					t.Errorf("exit code = %d, want 5 (a conflict for a person): %s", code, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("merge failed: %v\n%s", err, out)
			}
			merged := readEntries(t, filepath.Join(repo, packPath))
			for _, p := range c.present {
				if !has(merged, p) {
					t.Errorf("%s is missing from the merged pack: %v", p, merged)
				}
			}
			for _, p := range c.absent {
				if has(merged, p) {
					t.Errorf("%s came back from the dead: %v", p, merged)
				}
			}
			for p, wantJSON := range c.equal {
				got, ok := at(merged, p)
				if !ok {
					t.Errorf("%s is missing from the merged pack: %v", p, merged)
					continue
				}
				var want any
				if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
					t.Fatalf("the expectation for %s is not valid JSON: %v\n%s", p, err, wantJSON)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s merged to %s, want %s", p, mustJSON(t, got), wantJSON)
				}
			}
		})
	}
}

// The sweep does not run the script by hand: .gitattributes names it as the merge
// driver for the data tree, so a pack file that two branches both touched never
// goes through git's LINE merge at all.
//
// That is not a convenience. A pack file is a map, and the line merge can leave
// one entry key in it TWICE - which is not pack storage at all: pkg/pack refuses
// the file, metafmt cannot re-render it, and the branch is stuck. The fixture
// below is the shape that did it on 2026-08-09 (issue #1695), reduced from the
// real rebase: the pack's previous entry gained a recaps member on main (its own
// intake pull request merged), main also added the entry this branch is adding,
// with the other member. Each side's insertion is then anchored at a different
// line, git applies both, and the entry is in the file twice with one member
// apiece. Without that first ingredient the two insertions collide and git stops,
// which is why most of the sibling pull requests rebased cleanly.
func addAddCommunityFixture(t *testing.T) (base, main, branch string) {
	t.Helper()
	previous := charsMember("aaa", "Ann")
	return canonicalPack(t, pack(community("aaa", previous))),
		canonicalPack(t, pack(
			community("aaa", previous, recapsMember("aaa", "So far in aaa")),
			community("bbb", charsMember("bbb", "Bob")))),
		canonicalPack(t, pack(
			community("aaa", previous),
			community("bbb", recapsMember("bbb", "So far in bbb"))))
}

// TestLineMergeDoesNotConvergeOnAnAddAddPackConflict pins the hazard the merge
// driver exists for, on the fixture that hit it live.
func TestLineMergeDoesNotConvergeOnAnAddAddPackConflict(t *testing.T) {
	requireTools(t, "git")
	base, main, branch := addAddCommunityFixture(t)

	repo, out, err := rebaseFixture(t, communityPath, base, main, branch, "")
	if err != nil {
		// A future git that anchors both insertions at the same line stops here
		// instead, which the sweep already handles by running the script.
		t.Logf("git stopped on the conflict rather than duplicating the entry:\n%s", out)
		return
	}
	if n := countEntryKeys(t, filepath.Join(repo, communityPath), "bbb"); n < 2 {
		t.Fatalf("the line merge produced %d copies of the entry; it converged, so the fixture no longer models issue #1695", n)
	}
}

// driverMergedMarker is what .github/workflows/intake.yml greps a rebase's output
// for. It is the sweep's ONLY signal that a clean rebase merged a pack file after
// all, and so needs metafmt to re-render and re-place what the driver wrote; if
// the line stops reaching the rebase output - a reworded echo, a git that stops
// surfacing a driver's stdout - that step silently disarms and the branch is
// pushed un-normalized. The driver subtests assert it, so it fails here instead.
const driverMergedMarker = "merged both sides"

// TestPackMergeDriver runs the script the way the intake sweep runs it: as the
// merge driver .gitattributes names, so the entry maps are merged before any line
// merge can duplicate a key.
func TestPackMergeDriver(t *testing.T) {
	requireTools(t, "git", "jq")
	script := abs(t, "pack-union-merge.sh")
	base, main, branch := addAddCommunityFixture(t)

	t.Run("an entry both sides added keeps both members", func(t *testing.T) {
		repo, out, err := rebaseFixture(t, communityPath, base, main, branch, script)
		if err != nil {
			t.Fatalf("the driver did not resolve the conflict: %v\n%s", err, out)
		}
		if !strings.Contains(out, driverMergedMarker) {
			t.Errorf("the rebase output does not carry %q, which .github/workflows/intake.yml greps for to know it must re-render the merge:\n%s", driverMergedMarker, out)
		}
		full := filepath.Join(repo, communityPath)
		if n := countEntryKeys(t, full, "bbb"); n != 1 {
			t.Fatalf("the entry is in the pack %d times, want 1", n)
		}
		merged := readEntries(t, full)
		for _, p := range []string{"aaa.characters", "aaa.recaps", "bbb.characters", "bbb.recaps"} {
			if !has(merged, p) {
				t.Errorf("%s is missing from the merged pack: %v", p, merged)
			}
		}
	})

	t.Run("a works entry merges its recordings", func(t *testing.T) {
		repo, out, err := rebaseFixture(t, worksPath,
			canonicalPack(t, pack(workWithRecs("aaa", "A", rec("r1", "")))),
			canonicalPack(t, pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", "")))),
			canonicalPack(t, pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r3", "")))),
			script)
		if err != nil {
			t.Fatalf("the driver did not resolve the conflict: %v\n%s", err, out)
		}
		if !strings.Contains(out, driverMergedMarker) {
			t.Errorf("the rebase output does not carry %q, which .github/workflows/intake.yml greps for to know it must re-render the merge:\n%s", driverMergedMarker, out)
		}
		merged := readEntries(t, filepath.Join(repo, worksPath))
		for _, p := range []string{"aaa.recordings.r1", "aaa.recordings.r2", "aaa.recordings.r3"} {
			if !has(merged, p) {
				t.Errorf("%s is missing from the merged pack: %v", p, merged)
			}
		}
	})

	t.Run("a person both sides minted keeps both sources", func(t *testing.T) {
		// The live shape from the overlapping library exports, through the
		// driver rather than by hand: main has already merged the first import,
		// and the branch is the second one, which minted the same person off
		// whichever of his books it saw first.
		neighbour := entry("aaa", `"name":"A"`, sources(source("user", "", "2026-01-01")))
		mint := func(ref, at string) string {
			return entry("bob", `"name":"Bob"`, sources(source("libex-import", ref, at)))
		}
		repo, out, err := rebaseFixture(t, peoplePath,
			canonicalPack(t, pack(neighbour)),
			canonicalPack(t, pack(neighbour, mint("B000000001", "2026-09-01"))),
			canonicalPack(t, pack(neighbour, mint("B000000002", "2026-09-02"))),
			script)
		if err != nil {
			t.Fatalf("the driver did not resolve the conflict: %v\n%s", err, out)
		}
		if !strings.Contains(out, driverMergedMarker) {
			t.Errorf("the rebase output does not carry %q, which .github/workflows/intake.yml greps for to know it must re-render the merge:\n%s", driverMergedMarker, out)
		}
		full := filepath.Join(repo, peoplePath)
		if n := countEntryKeys(t, full, "bob"); n != 1 {
			t.Fatalf("the entry is in the pack %d times, want 1", n)
		}
		merged := readEntries(t, full)
		got, ok := at(merged, "bob.sources")
		if !ok {
			t.Fatalf("the merged person has no sources: %v", merged)
		}
		want := `[` + source("libex-import", "B000000001", "2026-09-01") + `,` +
			source("libex-import", "B000000002", "2026-09-02") + `]`
		var wantAny any
		if err := json.Unmarshal([]byte(want), &wantAny); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, wantAny) {
			t.Errorf("bob.sources merged to %s, want %s", mustJSON(t, got), want)
		}
	})

	t.Run("a series only main changed merges beside the branch's additions", func(t *testing.T) {
		base, main, branch, _ := issue2338Fixture(t)
		repo, out, err := rebaseFixture(t, seriesPath, base, main, branch, script)
		if err != nil {
			t.Fatalf("the driver did not resolve the conflict: %v\n%s", err, out)
		}
		merged := readEntries(t, filepath.Join(repo, seriesPath))
		for _, p := range []string{"harem-university", "hart-ranch"} {
			if !has(merged, p) {
				t.Errorf("%s is missing from the merged pack: %v", p, merged)
			}
		}
		works, _ := at(merged, "h-p-lovecrafts-schriften-des-grauens.works")
		if list, ok := works.([]any); !ok || len(list) != 4 {
			t.Errorf("main's appended volumes did not survive: %s", mustJSON(t, works))
		}
	})

	t.Run("a member both sides wrote still stops the rebase", func(t *testing.T) {
		repo, _, err := rebaseFixture(t, communityPath,
			canonicalPack(t, pack(community("aaa", charsMember("aaa", "Ann")))),
			canonicalPack(t, pack(community("aaa", charsMember("aaa", "Ann from main")))),
			canonicalPack(t, pack(community("aaa", charsMember("aaa", "Ann from the branch")))),
			script)
		if err == nil {
			t.Fatal("the driver merged two accounts of one member; it must leave that to a person")
		}
		// The sweep then runs the script over the conflicted file, which refuses
		// it the same way and reports the pull request.
		out, err := runIn(repo, script, communityPath)
		if code := exitCode(err); code != 5 {
			t.Errorf("exit code = %d, want 5 (a conflict for a person): %s", code, out)
		}
	})
}

// issue2338Fixture is the series pack PR #2338 could not be rebased with, reduced
// to its three entries: main (the daily sync bot) appended volumes 24 and 25 to
// one series, and the intake branch added two other series to the same pack
// while leaving that one byte-for-byte as the base had it.
func issue2338Fixture(t *testing.T) (base, main, branch, merged string) {
	t.Helper()
	const grauens = "h-p-lovecrafts-schriften-des-grauens"
	series := func(positions ...string) string {
		works := make([]string, 0, len(positions))
		for _, p := range positions {
			works = append(works, seriesWork("grauens-"+p, p))
		}
		return entry(grauens, `"name":"H. P. Lovecrafts Schriften des Grauens"`, seriesWorks(works...))
	}
	harem := entry("harem-university", `"name":"Harem University"`, seriesWorks(seriesWork("hu-1", "1")))
	hart := entry("hart-ranch", `"name":"Hart Ranch"`, seriesWorks(seriesWork("hr-1", "1")))
	return canonicalPack(t, pack(series("22", "23"))),
		canonicalPack(t, pack(series("22", "23", "24", "25"))),
		canonicalPack(t, pack(series("22", "23"), harem, hart)),
		canonicalPack(t, pack(series("22", "23", "24", "25"), harem, hart))
}

// TestPackMergeDriverTakesTheOnlyChangedSide calls the script exactly as git calls
// a merge driver (%O %A %B %P) on the #2338 shape: every change of both sides
// survives and the result is written to %A, where git reads it.
func TestPackMergeDriverTakesTheOnlyChangedSide(t *testing.T) {
	requireTools(t, "jq")
	script := abs(t, "pack-union-merge.sh")
	base, main, branch, want := issue2338Fixture(t)

	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	o, a, b := write("base", base), write("ours", main), write("theirs", branch)

	out, err := runIn(dir, script, o, a, b, "data/series/green-creek-italian-edition.json")
	if err != nil {
		t.Fatalf("the driver refused a pack only one side changed each entry of: %v\n%s", err, out)
	}
	if !strings.Contains(out, driverMergedMarker) {
		t.Errorf("the driver output does not carry %q:\n%s", driverMergedMarker, out)
	}
	var got, wantDoc any
	if err := json.Unmarshal([]byte(readFile(t, a)), &got); err != nil {
		t.Fatalf("the merged file is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantDoc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, wantDoc) {
		t.Errorf("merged to %s\nwant %s", mustJSON(t, got), mustJSON(t, wantDoc))
	}
}

// TestPackMergeRefusesANonPackFile is the guard on what this script may be handed.
// It merges the ENTRIES map, and jq's `//` reads a file with no "entries" as an
// empty one, so before the refusal it "merged" the tree's one non-pack file - the
// slug tombstone table - into {"entries":{}} and exited 0: a clean auto-merge that
// silently replaced every redirect with nothing.
//
// Both doors are checked, because both are reachable: the driver (a stale
// checkout's .gitattributes, which is exactly the checkout most likely to still
// route this path to it) and the by-hand invocation.
func TestPackMergeRefusesANonPackFile(t *testing.T) {
	requireTools(t, "git", "jq")
	script := abs(t, "pack-union-merge.sh")

	redirects := func(works string) string {
		return `{"people":{},"series":{},"works":{` + works + `}}` + "\n"
	}
	base := redirects(`"old-one":"live-one"`)
	main := redirects(`"old-one":"live-one","old-two":"live-two"`)
	branch := redirects(`"old-one":"live-one","old-three":"live-three"`)

	// The driver is configured for this path deliberately, overriding
	// .gitattributes' own exclusion: the point is that the script refuses even when
	// something hands it the file.
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := runIn(repo, "git", args...)
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.com")
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")
	git("config", "merge.packjson.name", "three-way merge of pack-file entries")
	git("config", "merge.packjson.driver", script+" %O %A %B %P")

	write(".gitattributes", "/data/redirects.json merge=packjson\n")
	write(redirectsPath, base)
	git("add", "-A")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "branch")
	write(redirectsPath, branch)
	git("commit", "-qam", "branch")
	git("checkout", "-q", "main")
	write(redirectsPath, main)
	git("commit", "-qam", "main")
	git("checkout", "-q", "branch")

	out, err := runIn(repo, "git", "rebase", "main")
	if err == nil {
		t.Fatalf("the rebase reported success on a non-pack file:\n%s", out)
	}
	// Whatever the conflict looks like, the one thing that must never happen is the
	// file being replaced by an empty pack.
	got := readFile(t, filepath.Join(repo, redirectsPath))
	if strings.Contains(got, `"entries"`) {
		t.Errorf("the driver rewrote the tombstone table as a pack:\n%s", got)
	}
	if !strings.Contains(got, "old-one") {
		t.Errorf("the tombstone table lost its records:\n%s", got)
	}

	// And by hand, on the conflict the rebase left behind: exit 1 (cannot be
	// merged this way), naming the reason.
	out, err = runIn(repo, script, redirectsPath)
	if code := exitCode(err); code != 1 {
		t.Errorf("exit code = %d, want 1 (not a pack file): %s", code, out)
	}
	if !strings.Contains(out, "not a pack file") {
		t.Errorf("the refusal does not say why: %s", out)
	}
}

// TestPackMergeAttributeCoversEveryFamily: the driver is only reached through
// .gitattributes, so the pattern has to name all four families' packs.
func TestPackMergeAttributeCoversEveryFamily(t *testing.T) {
	requireTools(t, "git")
	repo, _, _ := rebaseFixture(t, worksPath,
		canonicalPack(t, pack(work("aaa", "A"))),
		canonicalPack(t, pack(work("aaa", "A"), work("mmm", "M"))),
		canonicalPack(t, pack(work("aaa", "A"), work("zzz", "Z"))),
		"")
	for _, p := range []string{
		worksPath,
		communityPath,
		peoplePath,
		seriesPath,
	} {
		out, err := runIn(repo, "git", "check-attr", "merge", "--", p)
		if err != nil {
			t.Fatalf("git check-attr: %v\n%s", err, out)
		}
		if !strings.Contains(out, "merge: packjson") {
			t.Errorf("%s is not routed to the pack merge driver: %s", p, strings.TrimSpace(out))
		}
	}
}

// canonicalPack re-renders a fixture the way the tree stores a pack: sorted keys,
// two-space indent, one field per line. The line merge's behaviour is a property
// of that shape, so a fixture on a single line would not model the tree.
func canonicalPack(t *testing.T, body string) string {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("the fixture is not valid JSON: %v\n%s", err, body)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(out) + "\n"
}

// countEntryKeys reports how many times the pack file names one entry key at the
// top level. encoding/json keeps the last of a duplicated key without a word, and
// a duplicated key is the corruption at issue, so this counts the file's lines.
func countEntryKeys(t *testing.T, path, key string) int {
	t.Helper()
	prefix := `    "` + key + `": `
	n := 0
	for _, line := range strings.Split(readFile(t, path), "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// conflictedRebase builds a repository whose branch rebase stops on a conflict in
// path, and returns the repository directory mid-rebase.
func conflictedRebase(t *testing.T, path, base, main, branch string) string {
	t.Helper()
	repo, _, err := rebaseFixture(t, path, base, main, branch, "")
	if err == nil {
		t.Fatalf("the rebase did not conflict, so there is nothing to merge")
	}
	return repo
}

// rebaseFixture builds a repository from the three versions of one pack file and
// rebases the branch onto main, returning the repository directory, the rebase's
// output and its error (nil when the rebase completed).
//
// The repository always carries the real .gitattributes, which names
// scripts/pack-union-merge.sh as the merge driver for the data tree. With driver
// empty that attribute has nothing behind it and git falls back to its own line
// merge, which is every checkout that has not configured the driver; passing the
// script configures it, which is what the intake sweep does.
func rebaseFixture(t *testing.T, path, base, main, branch, driver string) (string, string, error) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := runIn(repo, "git", args...)
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.com")
	// An inherited ~/.gitconfig with commit signing would make these prompt.
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")
	if driver != "" {
		git("config", "merge.packjson.name", "three-way merge of pack-file entries")
		git("config", "merge.packjson.driver", driver+" %O %A %B %P")
	}

	write(".gitattributes", readFile(t, abs(t, filepath.Join("..", ".gitattributes"))))
	write(path, base)
	git("add", "-A")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "branch")
	write(path, branch)
	git("commit", "-qam", "branch")
	git("checkout", "-q", "main")
	write(path, main)
	git("commit", "-qam", "main")
	git("checkout", "-q", "branch")

	out, err := runIn(repo, "git", "rebase", "main")
	return repo, out, err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// readEntries returns the merged pack's entries.
func readEntries(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Entries map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the merged file is not valid JSON: %v\n%s", err, raw)
	}
	return doc.Entries
}

// at resolves a dotted path ("aaa" or "aaa.recordings.r1") in the entries map.
func at(entries map[string]any, dotted string) (any, bool) {
	cur := any(entries)
	for _, part := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// has reports whether the entries map holds the dotted path.
func has(entries map[string]any, dotted string) bool {
	_, ok := at(entries, dotted)
	return ok
}

// mustJSON renders a merged value for a failure message.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s is not available", n)
		}
	}
}

// abs resolves a script in this directory to an absolute path, since the tests
// run it from a temporary repository.
func abs(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}
