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

const maxWatchSeries = 200

// A valid CSV cannot exceed this many bytes. Bounding the decompressed stream
// before splitting keeps a compact parameter from becoming a decompression
// bomb, while the series-count check below remains the public limit.
const maxSeriesParamBytes = maxWatchSeries*(model.MaxSlugLen+1) - 1

// encodeSeriesParam returns the compact spelling of a series list: raw DEFLATE,
// then unpadded base64url, prefixed by z: so it cannot be mistaken for CSV.
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

// decodeSeriesParam accepts either comma-separated slugs or encodeSeriesParam's
// compact spelling. It validates the decoded list after applying a hard byte
// bound, so both forms have the same 200-series contract.
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
