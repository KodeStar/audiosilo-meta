package format

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// twoPassCheck is CheckProfile as it was before the survey judged canonical
// form: pkg/canonical over the whole tree, then a structural survey that parsed
// every pack again. It is kept here as the reference the one-pass check must
// match line for line.
func twoPassCheck(t *testing.T, dataDir string, p pack.Profile) (Report, error) {
	t.Helper()
	rep := Report{Dir: dataDir}
	if err := refuseLegacy(dataDir, p); err != nil {
		return Report{}, err
	}
	nonCanonical, invalid, err := canonical.CheckTreeExcept(dataDir, p.Excluded())
	if err != nil {
		return Report{}, err
	}
	rep.NonCanonical, rep.Invalid = nonCanonical, invalid
	err = withPackedFamilies(dataDir, p, func(s *pack.Store, families []pack.Family) error {
		for _, f := range families {
			pend, perr := s.Pending(f)
			if perr != nil {
				return perr
			}
			mergePending(&rep.Pending, pend)
		}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	rep.dropUnreadableDuplicates()
	return rep, nil
}

// TestOneParseCheckMatchesTheTwoPassCheck pins that metafmt --check reports
// exactly what it did when it parsed every pack twice: the same lines in the
// same order, the same summary and advice, under every profile, over a tree
// holding one of everything the two passes each judge - canonical and
// non-canonical packs, a salvaged file, unreadable and invalid files under a
// family root, stray JSON outside every family (canonical and not, valid and
// not), an upper-case .JSON file the pack listing does not see, the tombstone
// table, and a family the core profile disclaims.
func TestOneParseCheckMatchesTheTwoPassCheck(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"people/0.json":              canonicalOf(packOf(keyed("aa", 4), keyed("zz", 4))),
		"people/mm.json":             packOf(keyed("mm", 4)),
		"people/nn-bad.json":         `{"entries":{`,
		"people/oo-bad.json":         `{"name":"a record, not a pack"}`,
		"people/deep/far.json":       packOf(keyed("pp", 4)),
		"people/UPPER.JSON":          `{"b":1,"a":2}`,
		"series/0.json":              canonicalOf(packOf(keyed("s1", 4))),
		"works/0/0.json":             packOf(keyed("w1", 4)),
		"works/0/w5.json":            canonicalOf(packOf(keyed("w5", 4))),
		"works-community/0/0.json":   packOf(keyed("w1", 4)),
		"redirects.json":             `{"works":{}}`,
		"stray.json":                 `{"z":1,"a":2}`,
		"stray-canonical.json":       canonicalOf(`{"a":1}`),
		"stray-invalid.json":         `{`,
		"nested/elsewhere/odd.json":  `[1,2]`,
		"people/0-not-json.txt":      "ignored",
		"works/0/w9-duplicate.json":  `{"entries":{"w9":{"id":"w9"},"w9":{"id":"w9"}}}`,
		"works/0/w8-null-entry.json": `{"entries":null}`,
	}
	for rel, body := range files {
		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
	}
	// The one-pass check is run on one worker and on many: the verdicts are
	// filled from a pool, and the report must not depend on its scheduling.
	for _, procs := range []int{1, 8} {
		restore := runtime.GOMAXPROCS(procs)
		for _, p := range []pack.Profile{pack.ProfileAll, pack.ProfileCore, pack.ProfileCommunity} {
			want, werr := twoPassCheck(t, dir, p)
			got, gerr := CheckProfile(dir, p)
			if (werr == nil) != (gerr == nil) || (werr != nil && werr.Error() != gerr.Error()) {
				t.Fatalf("%s: error %v, the two-pass check's %v", p, gerr, werr)
			}
			if !reflect.DeepEqual(got.Lines(), want.Lines()) {
				t.Errorf("%s, %d procs: lines\n%q\nwant\n%q", p, procs, got.Lines(), want.Lines())
			}
			if !reflect.DeepEqual(got.Invalid, want.Invalid) {
				t.Errorf("%s: invalid %q, want %q", p, got.Invalid, want.Invalid)
			}
			if got.Summary() != want.Summary() || got.Advice() != want.Advice() || got.Clean() != want.Clean() {
				t.Errorf("%s: summary %q / %q, want %q / %q", p, got.Summary(), got.Advice(), want.Summary(), want.Advice())
			}
		}
		runtime.GOMAXPROCS(restore)
	}
}

// An I/O failure is still the canonical walk's error, naming the file: the
// survey only records a file it cannot read as unreadable, so the walk is what
// meets it, exactly as when it ran first.
func TestOneParseCheckKeepsTheReadError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not deny reads here")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4)))
	locked := filepath.Join(dir, "people", "mm.json")
	writeFile(t, locked, packOf(keyed("mm", 4)))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	_, werr := twoPassCheck(t, dir, pack.ProfileAll)
	_, gerr := CheckProfile(dir, pack.ProfileAll)
	if werr == nil || gerr == nil || werr.Error() != gerr.Error() {
		t.Fatalf("error %v, want the two-pass check's %v", gerr, werr)
	}
}
