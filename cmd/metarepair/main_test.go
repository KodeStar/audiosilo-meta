package main

import "testing"

// The one RULE in this otherwise-wiring command: an explicitly empty --op/--subclass is an
// error, so `--op "$OP"` with an unset variable cannot quietly mean "every op" against a
// database this tool deletes records from. Leaving the flag off still means every op, which
// is the interactive default and is what the flag's help says.
func TestRepeatedFlagRefusesAnEmptyValue(t *testing.T) {
	for _, tc := range []struct {
		in    string
		want  []string
		isErr bool
	}{
		{in: "merge-works", want: []string{"merge-works"}},
		{in: "merge-works,merge-series", want: []string{"merge-works", "merge-series"}},
		{in: " merge-works , merge-series ", want: []string{"merge-works", "merge-series"}},
		{in: "", isErr: true},
		{in: "   ", isErr: true},
		{in: ",", isErr: true},
		{in: "merge-works,", isErr: true},
		{in: ",merge-works", isErr: true},
	} {
		var got repeated
		err := got.Set(tc.in)
		if tc.isErr {
			if err == nil {
				t.Errorf("Set(%q) = %v with no error, want a refusal", tc.in, []string(got))
			}
			continue
		}
		if err != nil {
			t.Errorf("Set(%q): %v", tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("Set(%q) = %v, want %v", tc.in, []string(got), tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("Set(%q) = %v, want %v", tc.in, []string(got), tc.want)
				break
			}
		}
	}
}

// Repeats accumulate, which is the other half of the flag's contract.
func TestRepeatedFlagAccumulates(t *testing.T) {
	var got repeated
	for _, v := range []string{"merge-works", "retitle-work"} {
		if err := got.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if want := "merge-works,retitle-work"; got.String() != want {
		t.Errorf("String() = %q, want %q", got.String(), want)
	}
}
