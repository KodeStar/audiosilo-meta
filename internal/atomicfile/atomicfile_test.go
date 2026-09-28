package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Files are renamed in the order given, and a failed rename removes the ones
// already placed and every temp file.
func TestCommitInOrder(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "sub", "a"), filepath.Join(dir, "b")
	fa, err := Stage(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := Stage(b)
	if err != nil {
		t.Fatal(err)
	}
	fa.Encode(map[string]string{"asin": "B0X"})
	fb.Write([]byte("row\n"))
	var order []string
	rename = func(from, to string) error { order = append(order, to); return os.Rename(from, to) }
	t.Cleanup(func() { rename = os.Rename })
	if err := CommitInOrder(fa, nil, fb); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != a || order[1] != b {
		t.Errorf("rename order = %v", order)
	}
	if got, _ := os.ReadFile(a); string(got) != `{"asin":"B0X"}`+"\n" {
		t.Errorf("a = %q", got)
	}

	fa, _ = Stage(filepath.Join(dir, "c"))
	fb, _ = Stage(filepath.Join(dir, "d"))
	rename = func(from, to string) error {
		if to == filepath.Join(dir, "d") {
			return errors.New("injected")
		}
		return os.Rename(from, to)
	}
	if err := CommitInOrder(fa, fb); err == nil {
		t.Fatal("the injected rename must fail the commit")
	}
	for _, p := range []string{"c", "d"} {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Errorf("%s survived a failed commit", p)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".atomicfile-*")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// A nil *File is an output nobody asked for.
func TestNilFileIsANoOp(t *testing.T) {
	f, err := StageIf("")
	if f != nil || err != nil {
		t.Fatalf("StageIf(\"\") = %v, %v", f, err)
	}
	f.Write([]byte("x"))
	f.Encode(1)
	f.Discard()
	if err := CommitInOrder(f); err != nil {
		t.Fatal(err)
	}
}
