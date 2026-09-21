package serve

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// maxWatchSeries and the byte bound below are the ONE expression of the
// contract's size limit: the site refuses to build a URL past it
// (site/src/lib/feed-url.ts) and openapi.json states it.

const maxWatchSeries = 200

// A valid CSV cannot exceed this many bytes. Bounding the decompressed stream
// before splitting keeps a compact parameter from becoming a decompression
// bomb, while the series-count check below remains the public limit.
const maxSeriesParamBytes = maxWatchSeries*(model.MaxSlugLen+1) - 1

// decodeSeriesParam accepts either comma-separated slugs or the COMPACT
// spelling the site builds (site/src/lib/feed-url.ts `compactSeries`: raw
// DEFLATE, then unpadded base64url, prefixed `z:` so it cannot be mistaken for
// CSV). It validates the decoded list after applying a hard byte bound, so both
// forms have the same 200-series contract. The server only ever DECODES - the
// encoder is the browser's - so the matching Go encoder lives in the test file,
// beside the round trip it exists for.
func decodeSeriesParam(raw string) ([]string, error) {
	if raw == "" {
		return nil, errors.New("s is required")
	}
	csv := raw
	if strings.HasPrefix(raw, "z:") {
		compressed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(raw, "z:"))
		if err != nil {
			return nil, errors.New("s has invalid base64url")
		}
		r := flate.NewReader(bytes.NewReader(compressed))
		decoded, readErr := io.ReadAll(io.LimitReader(r, maxSeriesParamBytes+1))
		closeErr := r.Close()
		if readErr != nil || closeErr != nil {
			return nil, errors.New("s has invalid deflate data")
		}
		if len(decoded) > maxSeriesParamBytes {
			return nil, fmt.Errorf("s contains more than %d series", maxWatchSeries)
		}
		csv = string(decoded)
	}
	series := strings.Split(csv, ",")
	if err := validateSeriesList(series); err != nil {
		return nil, err
	}
	return series, nil
}

func validateSeriesList(series []string) error {
	if len(series) == 0 || (len(series) == 1 && series[0] == "") {
		return errors.New("s is required")
	}
	if len(series) > maxWatchSeries {
		return fmt.Errorf("s contains more than %d series", maxWatchSeries)
	}
	for _, slug := range series {
		if !model.ValidSlug(slug) {
			return fmt.Errorf("s contains invalid series slug %q", slug)
		}
	}
	return nil
}
