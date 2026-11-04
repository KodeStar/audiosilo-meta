package redirects

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// works builds a table holding only works tombstones, from "old=new" pairs.
func works(pairs ...string) model.Redirects {
	r := model.NewRedirects()
	for _, p := range pairs {
		from, to, _ := strings.Cut(p, "=")
		r[model.RedirectWorks][from] = to
	}
	return r
}

func TestMerge(t *testing.T) {
	cases := []struct {
		name               string
		base, ours, theirs model.Redirects
		want               model.Redirects
		// clash is a fragment the refusal must carry; empty means a clean merge.
		clash string
	}{
		{
			name:   "each side adds its own tombstone",
			base:   works("a-old=a-new"),
			ours:   works("a-old=a-new", "m-old=m-new"),
			theirs: works("a-old=a-new", "n-old=n-new"),
			want:   works("a-old=a-new", "m-old=m-new", "n-old=n-new"),
		},
		{
			name:   "no base: both sides created the file",
			base:   nil,
			ours:   works("m-old=m-new"),
			theirs: works("n-old=n-new"),
			want:   works("m-old=m-new", "n-old=n-new"),
		},
		{
			name:   "both sides add the same tombstone",
			base:   works(),
			ours:   works("m-old=m-new"),
			theirs: works("m-old=m-new"),
			want:   works("m-old=m-new"),
		},
		{
			name:   "both sides add one slug to different targets",
			base:   works(),
			ours:   works("m-old=m-one"),
			theirs: works("m-old=m-two"),
			clash:  "works m-old: both sides added it, to different targets (ours m-one, theirs m-two)",
		},
		{
			name:   "both sides retarget one slug differently",
			base:   works("m-old=m-new"),
			ours:   works("m-old=m-one"),
			theirs: works("m-old=m-two"),
			clash:  "works m-old: both sides retargeted it",
		},
		{
			name:   "one side retargets, the other leaves it",
			base:   works("m-old=m-new"),
			ours:   works("m-old=m-new"),
			theirs: works("m-old=m-two"),
			want:   works("m-old=m-two"),
		},
		{
			name:   "one side deletes, the other leaves it: deleted",
			base:   works("d-old=d-new", "m-old=m-new"),
			ours:   works("m-old=m-new"),
			theirs: works("d-old=d-new", "m-old=m-new", "n-old=n-new"),
			want:   works("m-old=m-new", "n-old=n-new"),
		},
		{
			name:   "one side deletes, the other retargets: refused",
			base:   works("d-old=d-new"),
			ours:   works(),
			theirs: works("d-old=d-two"),
			clash:  "works d-old: ours deleted it, theirs retargeted it",
		},
		{
			name:   "the other way round",
			base:   works("d-old=d-new"),
			ours:   works("d-old=d-two"),
			theirs: works(),
			clash:  "works d-old: theirs deleted it, ours retargeted it",
		},
		{
			name:   "both sides delete",
			base:   works("d-old=d-new"),
			ours:   works(),
			theirs: works(),
			want:   works(),
		},
		{
			// The union of two valid tables is a chain; Add collapses it the way
			// it would had the two edits landed one after the other.
			name:   "a chain across the two sides collapses",
			base:   works(),
			ours:   works("aaa=bbb"),
			theirs: works("bbb=ccc"),
			want:   works("aaa=ccc", "bbb=ccc"),
		},
		{
			// Ours retired bbb into ccc (Add repointing aaa); theirs merged zzz
			// into bbb. zzz must land on ccc.
			name:   "theirs' tombstone onto a slug ours retired collapses",
			base:   works("aaa=bbb"),
			ours:   works("aaa=ccc", "bbb=ccc"),
			theirs: works("aaa=bbb", "zzz=bbb"),
			want:   works("aaa=ccc", "bbb=ccc", "zzz=ccc"),
		},
		{
			// Theirs dropped the kkk tombstone (kkk is a record again) and retired
			// aaa into it. aaa sorts first, so an Add made before the removal would
			// have collapsed it through the dropped tombstone onto yyy.
			name:   "an incoming tombstone is added after theirs' removals",
			base:   works("kkk=yyy"),
			ours:   works("kkk=yyy"),
			theirs: works("aaa=kkk"),
			want:   works("aaa=kkk"),
		},
		{
			name:   "a cycle across the two sides is refused",
			base:   works(),
			ours:   works("aaa=bbb"),
			theirs: works("bbb=aaa"),
			clash:  "theirs' works bbb -> aaa cannot be added to ours: works redirect bbb: target \"aaa\" already redirects back to it, which would be a cycle",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Merge(tc.base, tc.ours, tc.theirs)
			if tc.clash != "" {
				var mc *MergeConflict
				if !errors.As(err, &mc) {
					t.Fatalf("Merge = %v, %v; want a MergeConflict", got, err)
				}
				if !strings.Contains(err.Error(), tc.clash) {
					t.Errorf("refusal %q does not name %q", err, tc.clash)
				}
				return
			}
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Merge = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMergeKeepsNamespacesApart: a slug is an id within its family, so the same
// spelling in two namespaces is two tombstones, never a clash.
func TestMergeKeepsNamespacesApart(t *testing.T) {
	ours := model.NewRedirects()
	ours[model.RedirectWorks]["same-slug"] = "work-target"
	theirs := model.NewRedirects()
	theirs[model.RedirectPeople]["same-slug"] = "person-target"
	got, err := Merge(nil, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if got[model.RedirectWorks]["same-slug"] != "work-target" || got[model.RedirectPeople]["same-slug"] != "person-target" {
		t.Errorf("Merge = %v", got)
	}
}

// TestMergeDoesNotEditItsInputs: the driver parses three versions once and must
// be able to report what each side held after a refusal.
func TestMergeDoesNotEditItsInputs(t *testing.T) {
	ours, theirs := works("aaa=bbb"), works("bbb=ccc")
	before := [2]model.Redirects{clone(ours), clone(theirs)}
	if _, err := Merge(nil, ours, theirs); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual([2]model.Redirects{ours, theirs}, before) {
		t.Errorf("Merge edited its inputs: %v %v", ours, theirs)
	}
}

// TestRenderIsWriteBytes: the driver hands git Render's output, and metafmt
// --check judges the file by Write's.
func TestRenderIsWriteBytes(t *testing.T) {
	dir := t.TempDir()
	r := works("m-old=m-new")
	if err := Write(dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := Render(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != read(t, dir) {
		t.Errorf("Render = %q, Write wrote %q", got, read(t, dir))
	}
}

// slowMerge is the reference Merge is measured against: the three-way rules over
// the kept values, then EVERY kept tombstone rebuilt through Add.
func slowMerge(t *testing.T, base, ours, theirs model.Redirects) model.Redirects {
	t.Helper()
	out := model.NewRedirects()
	for _, kind := range model.RedirectKinds() {
		kept := map[string]string{}
		for _, slug := range unionKeys(base[kind], ours[kind], theirs[kind]) {
			b, o, th := lookup(base[kind], slug), lookup(ours[kind], slug), lookup(theirs[kind], slug)
			var keep target
			switch {
			case o == th, th == b:
				keep = o
			case o == b:
				keep = th
			default:
				t.Fatalf("the fixture clashes at %s", slug)
			}
			if keep.ok {
				kept[slug] = keep.v
			}
		}
		for _, slug := range slices.Sorted(maps.Keys(kept)) {
			if err := Add(out, kind, slug, kept[slug]); err != nil {
				t.Fatal(err)
			}
		}
	}
	return out
}

// largeFixture is n tombstones in the base, with each side's edits on top: ours
// adds and deletes, theirs adds, retargets, deletes, and retires both a target
// the base points at and one of ours' new targets - so the merge collapses chains
// from both directions.
func largeFixture(t *testing.T, n int) (base, ours, theirs model.Redirects) {
	t.Helper()
	base = model.NewRedirects()
	for i := range n {
		base[model.RedirectWorks][fmt.Sprintf("old-%06d", i)] = fmt.Sprintf("new-%06d", i)
	}
	ours, theirs = clone(base), clone(base)
	for _, step := range []struct {
		r        model.Redirects
		from, to string
	}{
		{ours, "ours-a", "ours-target"},
		{ours, "ours-b", "new-000003"},
		{theirs, "theirs-a", "theirs-target"},
		{theirs, "new-000005", "theirs-survivor"},
		{theirs, "ours-target", "theirs-survivor"},
	} {
		if err := Add(step.r, model.RedirectWorks, step.from, step.to); err != nil {
			t.Fatal(err)
		}
	}
	delete(ours[model.RedirectWorks], "old-000001")
	delete(theirs[model.RedirectWorks], "old-000002")
	theirs[model.RedirectWorks]["old-000004"] = "elsewhere"
	return base, ours, theirs
}

// TestMergeMatchesTheSlowPath: starting from ours and adding only theirs' changes
// is the table every kept tombstone rebuilt through Add would be.
func TestMergeMatchesTheSlowPath(t *testing.T) {
	base, ours, theirs := largeFixture(t, 300)
	got, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if want := slowMerge(t, base, ours, theirs); !reflect.DeepEqual(got, want) {
		t.Errorf("Merge differs from the slow path")
	}
	w := got[model.RedirectWorks]
	for from, to := range map[string]string{
		"old-000005": "theirs-survivor", "new-000005": "theirs-survivor",
		"ours-a": "theirs-survivor", "ours-target": "theirs-survivor",
		"old-000004": "elsewhere", "ours-b": "new-000003",
	} {
		if w[from] != to {
			t.Errorf("%s -> %q, want %q", from, w[from], to)
		}
	}
	for _, gone := range []string{"old-000001", "old-000002"} {
		if _, ok := w[gone]; ok {
			t.Errorf("%s survived a deletion", gone)
		}
	}
}

// TestMergeLargeTableIsFast: the driver runs on every rebase that touches the
// file, so its cost must not grow with the square of the table. Rebuilding 20k
// tombstones through Add took seconds; the bound is generous for -race.
func TestMergeLargeTableIsFast(t *testing.T) {
	base, ours, theirs := largeFixture(t, 20000)
	start := time.Now()
	got, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("merging a 20k-entry table took %v", d)
	}
	if n := len(got[model.RedirectWorks]); n != 20000+5-2 {
		t.Errorf("merged %d tombstones, want %d", n, 20000+5-2)
	}
	if got[model.RedirectWorks]["ours-a"] != "theirs-survivor" {
		t.Errorf("the cross-side chain did not collapse: ours-a -> %q", got[model.RedirectWorks]["ours-a"])
	}
}
