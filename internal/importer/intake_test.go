package importer

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
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
		var work struct {
			Sources []model.Source `json:"sources"`
		}
		readEntity(t, dataDir, "works/th/the-lost-cartographer/work.json", &work)
		if len(work.Sources) != 1 || work.Sources[0].Type != "libex-import" || work.Sources[0].Ref != tc.ref {
			t.Errorf("intake %v: work sources = %+v, want one libex-import %s", tc.intake, work.Sources, tc.ref)
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

// TestIntakeDropWarningNamesTheASIN: the lane runs existing-series-only, and
// the warning for a claim it drops names the row by its ASIN, which is what a
// maintainer looks the row up by, not by the lane's stamp.
func TestIntakeDropWarningNamesTheASIN(t *testing.T) {
	sum := runLibexWith(t, cordellLoganTree(t), Options{ExistingSeriesOnly: true, Intake: true}, chuteLibre)
	if !hasWarning(sum.Warnings, `"Une Enquête de Cordell Logan" (B0GDJN7NZQ)`) {
		t.Errorf("the drop warning does not name the row's ASIN: %v", sum.Warnings)
	}
}
