package serve

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

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
