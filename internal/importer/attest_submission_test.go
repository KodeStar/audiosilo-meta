package importer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// AttestAt is the hand-submission door onto the user-overwrite rule: the intake
// bot's add-work / add-recording forms naming a catalogued ASIN. These tests pin
// that it IS the create path's attestation (attestExisting), not a copy of it:
// the same overwrite on a mirror seed, the same refusal on a disagreement, the
// same silence on an attested record. Most drive it through attestWholeLoad, which
// finds the ASIN by loading the catalogue; TestAttestAtMatchesAWholeLoad pins that
// AttestAt's seeding reaches the same result without that load.

// attestWholeLoad is AttestAt with the catalogue loaded to find the ASIN's
// recording and every taken ISBN - the whole-load reference AttestAt's seeding is
// pinned against.
func attestWholeLoad(a Attestation, opts Options) (Summary, error) {
	asin, err := a.check()
	if err != nil {
		return Summary{}, err
	}
	p, err := openPlanner(a.Source.Type, opts)
	if err != nil {
		return Summary{}, err
	}
	p.loadExisting()
	if _, located := p.asinLoc[asin]; !located {
		return p.result(), fmt.Errorf("attest: ASIN %s is not in the catalogue", asin)
	}
	return p.attest(a, asin, opts)
}

// formSource is the provenance a form submission stamps: `user`, citing what the
// submitter wrote in the Sources field.
var formSource = OutSource{Type: "user", Ref: "my own copy", ImportedAt: testImportDate}

func TestAttestOverwritesAMirrorSeedAndTakesOverItsProvenance(t *testing.T) {
	// The work carries a mirror genre, so the union (rule 5) is visible.
	dataDir := seedTierTree(t, map[string]string{
		tierWorkRel: strings.Replace(tierWork, `"id":"the-lost-cartographer"`,
			`"genres":["fantasy"],"id":"the-lost-cartographer"`, 1),
	})
	sum, err := attestWholeLoad(Attestation{
		ASIN: "b0libex001", Source: formSource,
		RuntimeMin: 605, ReleaseDate: "2019-05-04", Publisher: "Lost Press",
		CoverURL: "https://m.media-amazon.com/images/I/user.jpg",
		ISBNs:    []string{"9780000000019"}, Genres: []string{"action-adventure"},
	}, Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	if sum.AttestedRecordings != 1 || sum.AttestedWorks != 1 || sum.Conflicts != 0 {
		t.Fatalf("Attested recordings/works/conflicts = %d/%d/%d, want 1/1/0",
			sum.AttestedRecordings, sum.AttestedWorks, sum.Conflicts)
	}
	if sum.NewWorks+sum.NewRecordings+sum.NewPeople+sum.NewSeries != 0 {
		t.Errorf("an attestation creates nothing: %+v", sum)
	}
	if len(sum.Files) == 0 {
		t.Error("the summary must name the packs it rewrote")
	}

	var rec recordingFile
	readEntity(t, dataDir, tierRecRel, &rec)
	if rec.Publisher != "Lost Press" || rec.ReleaseDate != "2019-05-04" || rec.RuntimeMin != 605 ||
		rec.CoverURL != "https://m.media-amazon.com/images/I/user.jpg" {
		t.Errorf("the stated facts must replace the mirror's: %+v", rec)
	}
	if len(rec.ISBN) != 1 || rec.ISBN[0] != "9780000000019" {
		t.Errorf("isbn = %v, want the submitted one appended", rec.ISBN)
	}
	// Silence is not an assertion: the chapter table the form cannot state stays.
	if len(rec.Chapters) != 1 || rec.Chapters[0].Title != "Mirror Chapter" {
		t.Errorf("chapters = %+v, want the mirror's kept", rec.Chapters)
	}
	if len(rec.Sources) != 2 || rec.Sources[1].Type != "user" || rec.Sources[1].Ref != "my own copy" {
		t.Errorf("recording sources = %+v, want the submission's own entry appended", rec.Sources)
	}

	var work enrichedWork
	readEntity(t, dataDir, tierWorkRel, &work)
	if strings.Join(work.Genres, ",") != "action-adventure,fantasy" {
		t.Errorf("genres = %v, want the submission's ADDED to the mirror's", work.Genres)
	}
	if len(work.Sources) != 2 || work.Sources[1].Type != "user" {
		t.Errorf("work sources = %+v, want the user stamp appended", work.Sources)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("attested tree failed validation:\n%v", res.Problems)
	}
}

// A runtime more than 10% apart is GOVERNANCE's disagreement: the row is refused
// whole, nothing is written - not even the stamp that would end the mirror-only
// status - and the conflict names both values for the maintainer.
func TestAttestDisagreementWritesNothingAndIsCounted(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	before := readRaw(t, dataDir, tierRecRel)
	sum, err := attestWholeLoad(Attestation{
		ASIN: "B0LIBEX001", Source: formSource, RuntimeMin: 400, Publisher: "Somebody Else",
	}, Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	if sum.Conflicts != 1 || sum.Produced() != 0 {
		t.Fatalf("conflicts/produced = %d/%d, want 1/0", sum.Conflicts, sum.Produced())
	}
	if !strings.Contains(strings.Join(sum.RowWarnings(), "\n"), "runtime 400 min conflicts with the recorded 600 min") {
		t.Errorf("the warning must name both values: %v", sum.Warnings)
	}
	if after := readRaw(t, dataDir, tierRecRel); after != before {
		t.Errorf("a refused attestation must write nothing:\nbefore %s\nafter  %s", before, after)
	}
}

// A release date that is not the same date at another precision disagrees too;
// the same date at another precision does not.
func TestAttestReleaseDateDisagreement(t *testing.T) {
	dataDir := seedTierTree(t, nil) // recorded "2019"
	sum, err := attestWholeLoad(Attestation{ASIN: "B0LIBEX001", Source: formSource, ReleaseDate: "2020-01-01"},
		Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	if sum.Conflicts != 1 || sum.Produced() != 0 {
		t.Fatalf("conflicts/produced = %d/%d, want 1/0", sum.Conflicts, sum.Produced())
	}
}

// After the first attestation the ordinary rules resume: first writer wins, and
// an agreeing second submission writes nothing at all.
func TestAttestLeavesAnAttestedRecordAlone(t *testing.T) {
	dataDir := seedTierTree(t, map[string]string{
		tierRecRel:  tierRecordingAttested,
		tierWorkRel: tierWorkAttested,
	})
	before := readRaw(t, dataDir, tierRecRel)
	sum, err := attestWholeLoad(Attestation{ASIN: "B0LIBEX001", Source: formSource, Publisher: "A Third Imprint"},
		Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("attest: %v", err)
	}
	if sum.Produced() != 0 || sum.Conflicts != 0 {
		t.Fatalf("produced/conflicts = %d/%d, want 0/0", sum.Produced(), sum.Conflicts)
	}
	if after := readRaw(t, dataDir, tierRecRel); after != before {
		t.Errorf("an attested record must not be rewritten:\nbefore %s\nafter  %s", before, after)
	}
}

// AttestAt refuses what no attestation may apply - a source without overwrite
// authority, a malformed ASIN - before it touches the tree; the whole-load
// reference refuses an uncatalogued ASIN rather than creating anything.
func TestAttestRefusesWhatItCannotAttest(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	opts := Options{DataDir: dataDir, ImportDate: testImportDate}
	if _, err := attestWholeLoad(Attestation{ASIN: "B0NOTHERE1", Source: formSource}, opts); err == nil {
		t.Error("an uncatalogued ASIN must be refused, not created")
	}
	store, err := openStore(dataDir, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ref := RecRef{Work: "the-lost-cartographer", Rec: "bea-reader-2019"}
	libex := OutSource{Type: "libex-import", Ref: "B0LIBEX001", ImportedAt: testImportDate}
	if _, err := AttestAt(store, ref, nil, Attestation{ASIN: "B0LIBEX001", Source: libex, Publisher: "X"}, opts); err == nil {
		t.Error("a bulk-mirror source has no overwrite authority")
	}
	if _, err := AttestAt(store, ref, nil, Attestation{ASIN: "not-an-asin", Source: formSource}, opts); err == nil {
		t.Error("a malformed ASIN must be refused")
	}
}

// TestAttestAtMatchesAWholeLoad pins AttestAt's seeding against a whole
// catalogue load: over the same fixture - an overwrite, a genre union, and one
// submitted ISBN another recording already carries - both entry points must
// report the same Summary and write the same tree.
func TestAttestAtMatchesAWholeLoad(t *testing.T) {
	const takenISBN, freshISBN = "9781473647633", "9780000000019"
	overrides := map[string]string{
		tierWorkRel: strings.Replace(tierWork, `"id":"the-lost-cartographer"`,
			`"genres":["fantasy"],"id":"the-lost-cartographer"`, 1),
		"works/an/another-map/work.json": `{"authors":["ada-mapmaker"],"id":"another-map","language":"en",` +
			`"license":"CC0-1.0","sources":[{"type":"user"}],"title":"Another Map"}`,
		"works/an/another-map/recordings/bea-reader-2020.json": `{"id":"bea-reader-2020","isbn":["` + takenISBN + `"],` +
			`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"another-map"}`,
	}
	a := Attestation{
		ASIN: "B0LIBEX001", Source: formSource, RuntimeMin: 605, ReleaseDate: "2019-05-04",
		Publisher: "Lost Press &amp; Co", ISBNs: []string{takenISBN, freshISBN}, Genres: []string{"horror"},
	}

	whole := seedTierTree(t, overrides)
	wantSum, err := attestWholeLoad(a, Options{DataDir: whole, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("attest: %v", err)
	}

	seeded := seedTierTree(t, overrides)
	store, err := openStore(seeded, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	gotSum, err := AttestAt(store, RecRef{Work: "the-lost-cartographer", Rec: "bea-reader-2019"},
		[]string{takenISBN}, a, Options{DataDir: seeded, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("AttestAt: %v", err)
	}

	if !reflect.DeepEqual(gotSum, wantSum) {
		t.Errorf("summaries differ:\nAttestAt   %+v\nwhole load %+v", gotSum, wantSum)
	}
	if wantSum.AttestedRecordings != 1 || !strings.Contains(strings.Join(wantSum.Warnings, "\n"), takenISBN) {
		t.Errorf("the fixture must exercise the overwrite and the taken ISBN: %+v", wantSum)
	}
	if got, want := treeFiles(t, seeded), treeFiles(t, whole); !reflect.DeepEqual(got, want) {
		t.Errorf("the two entry points wrote different trees")
	}
	var rec recordingFile
	readEntity(t, seeded, tierRecRel, &rec)
	if rec.Publisher != "Lost Press &amp; Co" {
		t.Errorf("publisher = %q, want the submitted text stored as given: the form door decoded it already", rec.Publisher)
	}
}

// treeFiles reads every file under dir, keyed by its relative path.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}
