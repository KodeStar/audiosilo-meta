package serve

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"slices"
	"strings"
	"testing"
)

// encodeSeriesParam is the compact spelling's ENCODER. Production never needs
// one - the browser builds these URLs - so it lives here, as the round trip's
// other half: raw DEFLATE, then unpadded base64url, prefixed z:.
func encodeSeriesParam(series []string) (string, error) {
	if err := validateSeriesList(series); err != nil {
		return "", err
	}
	var compressed bytes.Buffer
	w, err := flate.NewWriter(&compressed, flate.DefaultCompression)
	if err != nil {
		return "", err
	}
	if _, err := io.WriteString(w, strings.Join(series, ",")); err != nil {
		_ = w.Close()
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return "z:" + base64.RawURLEncoding.EncodeToString(compressed.Bytes()), nil
}

func TestSeriesParamPlainAndCompact(t *testing.T) {
	want := []string{"the-stormlight-archive", "murderbot-diaries"}
	plain, err := decodeSeriesParam(strings.Join(want, ","))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plain, ",") != strings.Join(want, ",") {
		t.Fatalf("plain decode = %v, want %v", plain, want)
	}

	compact, err := encodeSeriesParam(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(compact, "z:") || strings.Contains(compact, "=") {
		t.Fatalf("compact parameter = %q, want z: and unpadded base64url", compact)
	}
	decoded, err := decodeSeriesParam(compact)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(decoded, ",") != strings.Join(want, ",") {
		t.Fatalf("compact decode = %v, want %v", decoded, want)
	}
}

// TestSeriesParamDecodesTheBrowsersEncoding is the ONE place the two halves of
// the compact form actually meet. The encoder that matters in production is the
// browser's (site/src/lib/feed-url.ts `compactSeries`: CompressionStream
// 'deflate-raw' -> btoa -> +/ replaced, padding stripped), and a Go test that
// only round-trips Go would pass just as happily if the two disagreed about the
// zlib header or the base64 alphabet.
//
// So this fixture is REAL browser output, produced once by running that helper
// under Node 24 on the CSV below and pasted here. Regenerate it the same way if
// the encoding ever changes - and if this test fails while the pure-Go round
// trip above passes, it is the wire format that has drifted, not the decoder.
func TestSeriesParamDecodesTheBrowsersEncoding(t *testing.T) {
	const fromBrowser = "z:BcGBCQAgCATAhXIoS0khEV5r_u7alKoTcXxbE2OZPx1xIYqZTeIM1_o"
	want := []string{"the-stormlight-archive", "murderbot-diaries"}
	got, err := decodeSeriesParam(fromBrowser)
	if err != nil {
		t.Fatalf("decoding the browser's compact parameter: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("decoded %v, want %v", got, want)
	}
}

func TestSeriesParamRejectsBadCompactValuesAndLimits(t *testing.T) {
	for _, raw := range []string{"", "z:not!base64", "z:bm90IGRlZmxhdGU", "valid,,slug"} {
		if _, err := decodeSeriesParam(raw); err == nil {
			t.Errorf("decodeSeriesParam(%q) succeeded", raw)
		}
	}
	tooMany := make([]string, maxWatchSeries+1)
	for i := range tooMany {
		tooMany[i] = "series"
	}
	if _, err := decodeSeriesParam(strings.Join(tooMany, ",")); err == nil {
		t.Errorf("decoded %d series, want an error", len(tooMany))
	}
	var compressed bytes.Buffer
	w, err := flate.NewWriter(&compressed, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, strings.Join(tooMany, ",")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	compact := "z:" + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	if _, err := decodeSeriesParam(compact); err == nil {
		t.Errorf("decoded %d compact series, want an error", len(tooMany))
	}
}
