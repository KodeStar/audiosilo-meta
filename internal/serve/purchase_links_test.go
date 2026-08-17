package serve

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

func TestDerivedPurchaseLinks(t *testing.T) {
	links := derivedPurchaseLinks(
		[]asinRef{{Region: "us", ASIN: "B0794BXZBF"}},
		[]string{"9781478975311", "123456789X", "9781478975310"},
	)
	if len(links) != 2 {
		t.Fatalf("links = %+v, want Audible plus the one checksum-valid ISBN-13 candidate", links)
	}
	if got := links[0]; got.Retailer != "audible" || got.Region != "us" ||
		got.ID != "B0794BXZBF" || got.URL != "https://www.audible.com/pd/B0794BXZBF" ||
		got.Availability != "unknown" {
		t.Errorf("Audible link = %+v", got)
	}
	if got := links[1]; got.Retailer != "libro-fm" || got.Region != "" ||
		got.ID != "9781478975311" || got.URL != "https://libro.fm/audiobooks/9781478975311" ||
		got.Availability != "unknown" {
		t.Errorf("Libro.fm link = %+v", got)
	}
}

// TestAudibleMarketplaceHostsCoverTheRegionEnum is the drift guard between the
// host table and the one region vocabulary (common.schema.json #/$defs/region),
// in BOTH directions: a region added to the enum with no host entry means
// derivedPurchaseLinks silently drops that marketplace's ASINs, and a host key
// outside the enum is dead data no recording can reach. The host VALUES have no
// second source of truth (the schema cannot know Audible's domains), so only
// the key set is compared.
func TestAudibleMarketplaceHostsCoverTheRegionEnum(t *testing.T) {
	want := testpack.SchemaDefEnum(t, "region")
	got := make(map[string]bool, len(audibleMarketplaceHosts))
	for region, host := range audibleMarketplaceHosts {
		if host == "" {
			t.Errorf("empty host for region %q", region)
		}
		got[region] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audibleMarketplaceHosts keys = %v, schema $defs/region = %v", got, want)
	}
}
