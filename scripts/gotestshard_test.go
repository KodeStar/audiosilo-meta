package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Tests of .github/scripts/go-test-shard.sh, which splits `go list ./...` over
// check.yml's `test` matrix, and of the matrix itself. The property that matters
// is coverage: across the shards of one count, every package runs exactly once.

const modulePath = "github.com/kodestar/audiosilo-meta"

// shard runs go-test-shard.sh with pkgs on stdin and returns its output lines
// and exit status.
func shard(t *testing.T, pkgs []string, args ...string) ([]string, int) {
	t.Helper()
	requireTools(t, "bash", "awk", "sort", "grep")
	out, code := runScriptInput(t, "", "go-test-shard.sh", strings.NewReader(strings.Join(pkgs, "\n")+"\n"), nil, nil, args...)
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, code
}

// fakePackages is a listing shaped like the module's: the weighted long
// packages plus many unweighted short ones, unsorted and with a duplicate.
func fakePackages() []string {
	pkgs := []string{
		modulePath + "/internal/recorddiff",
		modulePath + "/internal/importer",
		modulePath + "/internal/serve",
		modulePath + "/pkg/check",
	}
	for i := range 25 {
		pkgs = append(pkgs, fmt.Sprintf("%s/pkg/small%02d", modulePath, 24-i))
	}
	return append(pkgs, modulePath+"/pkg/check")
}

func TestGoTestShardCoversEveryPackageOnce(t *testing.T) {
	t.Parallel()
	pkgs := fakePackages()
	want := slices.Clone(pkgs)
	slices.Sort(want)
	want = slices.Compact(want)
	for count := 1; count <= 5; count++ {
		var all []string
		for i := 1; i <= count; i++ {
			got, code := shard(t, pkgs, fmt.Sprint(i), fmt.Sprint(count))
			if code != 0 {
				t.Fatalf("shard %d of %d exited %d", i, count, code)
			}
			if len(got) == 0 {
				t.Errorf("shard %d of %d is empty", i, count)
			}
			if !slices.IsSorted(got) {
				t.Errorf("shard %d of %d is not sorted: %v", i, count, got)
			}
			// The same shard asked twice answers the same: a matrix job and a
			// re-run of it must test the same packages.
			if again, _ := shard(t, pkgs, fmt.Sprint(i), fmt.Sprint(count)); !slices.Equal(again, got) {
				t.Errorf("shard %d of %d is not deterministic: %v then %v", i, count, got, again)
			}
			all = append(all, got...)
		}
		slices.Sort(all)
		if !slices.Equal(all, want) {
			t.Errorf("the %d shards together run\n%v\nwant every package exactly once:\n%v", count, all, want)
		}
	}
}

// The two long packages never share a shard once there are two, or the slowest
// shard is their sum and the split buys nothing.
func TestGoTestShardSeparatesTheLongPackages(t *testing.T) {
	t.Parallel()
	for count := 2; count <= 4; count++ {
		for i := 1; i <= count; i++ {
			got, _ := shard(t, fakePackages(), fmt.Sprint(i), fmt.Sprint(count))
			if slices.Contains(got, modulePath+"/internal/importer") && slices.Contains(got, modulePath+"/internal/recorddiff") {
				t.Errorf("shard %d of %d holds both long packages: %v", i, count, got)
			}
		}
	}
}

func TestGoTestShardRefusesBadInput(t *testing.T) {
	t.Parallel()
	pkgs := fakePackages()
	for _, args := range [][]string{{}, {"1"}, {"0", "3"}, {"4", "3"}, {"1", "0"}, {"x", "3"}, {"1", "3", "extra"}} {
		if _, code := shard(t, pkgs, args...); code != 2 {
			t.Errorf("go-test-shard.sh %v exited %d, want 2", args, code)
		}
	}
	// An empty listing is a failure, never an empty (passing) shard.
	if got, code := shard(t, nil, "1", "3"); code != 1 || len(got) != 0 {
		t.Errorf("empty stdin = %v, exit %d; want nothing and exit 1", got, code)
	}
	// So is a shard the partition leaves empty (more shards than packages):
	// `xargs go test` over nothing would test one directory and pass.
	two := []string{modulePath + "/pkg/a", modulePath + "/pkg/b"}
	if got, code := shard(t, two, "3", "3"); code != 1 || len(got) != 0 {
		t.Errorf("empty shard 3 of 3 = %v, exit %d; want nothing and exit 1", got, code)
	}
}

// Every package the WEIGHTS table names still exists: a weight left behind by
// a rename steers nothing and reads as if the package were balanced.
func TestGoTestShardWeightsNameRealPackages(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", ".github", "scripts", "go-test-shard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(modulePath) + `/(\S+) \d+$`)
	matches := re.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		t.Fatal("found no weighted package in go-test-shard.sh")
	}
	for _, m := range matches {
		dir := filepath.Join("..", filepath.FromSlash(string(m[1])))
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			t.Errorf("go-test-shard.sh weighs %s, which is not a package directory (%v)", m[1], err)
		}
	}
}

// check.yml's `test` matrix lists exactly the shards 1..N, and its step passes
// the matrix's own size (strategy.job-total) as the count, so no shard of the
// partition goes unrun and none is asked for twice.
func TestCheckWorkflowRunsEveryShard(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "check.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^        shard: \[([0-9, ]+)\]$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("found no `shard: [...]` matrix in check.yml")
	}
	var shards, want []string
	for i, s := range strings.Split(string(m[1]), ",") {
		shards = append(shards, strings.TrimSpace(s))
		want = append(want, fmt.Sprint(i+1))
	}
	if !slices.Equal(shards, want) {
		t.Errorf("check.yml's matrix runs shards %v, want %v", shards, want)
	}
	if calls := regexp.MustCompile(`go-test-shard\.sh (\S+ \S+)`).FindAllSubmatch(raw, -1); len(calls) != 1 || string(calls[0][1]) != `"$SHARD" "$SHARDS"` {
		t.Errorf("check.yml's go-test-shard.sh calls = %q, want one passing \"$SHARD\" \"$SHARDS\"", calls)
	}
	if !strings.Contains(string(raw), "SHARDS: ${{ strategy.job-total }}") {
		t.Error("check.yml's test step does not take SHARDS from strategy.job-total")
	}
}
