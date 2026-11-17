package importer

import (
	"strings"
	"testing"
)

// TestLibexIntakeStampsTheLane: a reported-ASIN lane run (Options.Intake) stamps
// every record it writes `libex-import` with the ref "intake:<ASIN>", so the
// lane's records are one query apart from the feeds' (meta#2890); the same rows
// without it stamp the bare ASIN, as every libex run always has.
func TestLibexIntakeStampsTheLane(t *testing.T) {
	export := libexFixture(t)
	for _, tc := range []struct {
		intake bool
		ref    string
	}{
		{false, "B0LIBEX001"},
		{true, IntakeRefPrefix + "B0LIBEX001"},
	} {
		dataDir := t.TempDir()
		if _, err := RunLibex(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: testImportDate, Intake: tc.intake}); err != nil {
			t.Fatalf("intake %v: %v", tc.intake, err)
		}
		type sourced struct {
			Sources []struct {
				Type string `json:"type"`
				Ref  string `json:"ref"`
			} `json:"sources"`
		}
		var work sourced
		readEntity(t, dataDir, "works/th/the-lost-cartographer/work.json", &work)
		if len(work.Sources) != 1 || work.Sources[0].Type != "libex-import" || work.Sources[0].Ref != tc.ref {
			t.Errorf("intake %v: work sources = %+v, want one libex-import %s", tc.intake, work.Sources, tc.ref)
		}
		for _, s := range work.Sources {
			if tc.intake != strings.HasPrefix(s.Ref, IntakeRefPrefix) {
				t.Errorf("intake %v: ref %q", tc.intake, s.Ref)
			}
		}
	}
}

// TestIntakeIsACreateRunOfLibexRows: the lane imports libex rows in the create
// pass; anywhere else the flag would stamp nothing it was asked to, so the run
// is refused before it reads a row.
func TestIntakeIsACreateRunOfLibexRows(t *testing.T) {
	export := writeBooks(t, libexFixture(t))
	if _, err := RunLibex(export, Options{DataDir: t.TempDir(), ImportDate: testImportDate, Intake: true, Mode: ModeEnrich}); err == nil {
		t.Error("an enrich run with Intake was not refused")
	}
	if _, err := Run(writeBooks(t, `[]`), Options{DataDir: t.TempDir(), ImportDate: testImportDate, Intake: true}); err == nil {
		t.Error("an OpenAudible run with Intake was not refused")
	}
}
