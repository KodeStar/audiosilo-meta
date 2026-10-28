package canonical

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// The tree walks judge one file per worker (internal/parallel), so what they
// report has to be a function of the tree and not of the scheduler. The fixture
// mixes canonical, non-canonical and unparseable files across several
// directories, more of them than there are workers, and the walks are run with
// one worker and with many: both must name the same files in walk order.

// parallelFixture writes the fixture under a fresh directory and returns it with
// the files it expects the walks to name, in walk (sorted path) order.
func parallelFixture(t *testing.T) (dir string, nonCanonical, invalid []string) {
	t.Helper()
	dir = t.TempDir()
	for i := range 48 {
		sub := filepath.Join(dir, fmt.Sprintf("d%d", i%4))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(sub, fmt.Sprintf("f%02d.json", i))
		var body string
		switch i % 3 {
		case 0:
			body = fmt.Sprintf("{\n  \"n\": %d\n}\n", i)
		case 1:
			body = fmt.Sprintf(`{"z":%d,"a":1}`, i)
			nonCanonical = append(nonCanonical, path)
		default:
			if i%2 == 0 {
				body = `{"broken":`
				nonCanonical = append(nonCanonical, path)
				invalid = append(invalid, path)
			} else {
				body = fmt.Sprintf("{\"n\":%d}", i)
				nonCanonical = append(nonCanonical, path)
			}
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(nonCanonical)
	slices.Sort(invalid)
	return dir, nonCanonical, invalid
}

func TestTreeWalkIsDeterministic(t *testing.T) {
	for _, procs := range []int{1, 8} {
		t.Run(fmt.Sprintf("GOMAXPROCS=%d", procs), func(t *testing.T) {
			restore := runtime.GOMAXPROCS(procs)
			t.Cleanup(func() { runtime.GOMAXPROCS(restore) })

			dir, wantNon, wantInvalid := parallelFixture(t)
			gotNon, gotInvalid, err := CheckTree(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(gotNon, wantNon) {
				t.Errorf("CheckTree nonCanonical =\n%v\nwant\n%v", gotNon, wantNon)
			}
			if !slices.Equal(gotInvalid, wantInvalid) {
				t.Errorf("CheckTree invalid =\n%v\nwant\n%v", gotInvalid, wantInvalid)
			}

			// A write rewrites exactly the parseable non-canonical files, in walk
			// order, and leaves every unparseable one byte-for-byte alone.
			before := map[string][]byte{}
			for _, f := range wantInvalid {
				raw, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				before[f] = raw
			}
			var wantChanged []string
			for _, f := range wantNon {
				if !slices.Contains(wantInvalid, f) {
					wantChanged = append(wantChanged, f)
				}
			}
			changed, failed, err := WriteTree(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(changed, wantChanged) {
				t.Errorf("WriteTree changed =\n%v\nwant\n%v", changed, wantChanged)
			}
			if !slices.Equal(failed, wantInvalid) {
				t.Errorf("WriteTree failed =\n%v\nwant\n%v", failed, wantInvalid)
			}
			for f, raw := range before {
				after, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				if string(after) != string(raw) {
					t.Errorf("%s does not parse and was rewritten anyway", f)
				}
			}
			after, afterInvalid, err := CheckTree(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(after, wantInvalid) || !slices.Equal(afterInvalid, wantInvalid) {
				t.Errorf("after the write only the unparseable files may be non-canonical; got %v / %v", after, afterInvalid)
			}
		})
	}
}

// A file the walk cannot read is an error, not a verdict: neither walk may
// report a partial list as though the tree had been judged.
func TestTreeWalkReportsAnUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes do not deny reads on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	restore := runtime.GOMAXPROCS(8)
	t.Cleanup(func() { runtime.GOMAXPROCS(restore) })

	dir, _, _ := parallelFixture(t)
	locked := filepath.Join(dir, "d1", "locked.json")
	if err := os.WriteFile(locked, []byte(`{"b":1,"a":2}`), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	if non, invalid, err := CheckTree(dir); err == nil {
		t.Errorf("CheckTree over an unreadable file = %v, %v, nil; want an error", non, invalid)
	}
	if changed, failed, err := WriteTree(dir); err == nil {
		t.Errorf("WriteTree over an unreadable file = %v, %v, nil; want an error", changed, failed)
	}
}
