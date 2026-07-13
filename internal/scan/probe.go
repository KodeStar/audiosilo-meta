package scan

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// probeResult is the subset of ffprobe output metascan consumes for a single file.
type probeResult struct {
	duration float64           // seconds
	chapters int               // embedded chapter count
	tags     map[string]string // lower-cased container tags
}

// ffprobeJSON is the shape of `ffprobe -print_format json` output we parse.
type ffprobeJSON struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		Tags map[string]string `json:"tags"`
	} `json:"streams"`
	Chapters []struct{} `json:"chapters"` // elements only counted, never read
}

// hasFFprobe reports whether an ffprobe binary is resolvable (on PATH, or at an
// explicit path).
func hasFFprobe(ffprobePath string) bool {
	if ffprobePath == "" {
		return false
	}
	_, err := exec.LookPath(ffprobePath)
	return err == nil
}

// probe runs ffprobe against one file for duration, chapter count, and
// container + stream tags. Best-effort: any failure returns (nil, err) and the
// caller degrades. MP4 stores language on the STREAM, not the container, so
// the first audio stream's language tag is folded into the flat tag map (m4b
// is the dominant audiobook container - format tags alone would never yield
// language for it).
func probe(path, ffprobePath string) (*probeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_chapters",
		"-show_streams",
		"-select_streams", "a:0",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var parsed ffprobeJSON
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	res := &probeResult{chapters: len(parsed.Chapters), tags: lowerTags(parsed.Format.Tags)}
	res.duration, _ = strconv.ParseFloat(parsed.Format.Duration, 64)
	if res.tags["language"] == "" && len(parsed.Streams) > 0 {
		if lang := strings.TrimSpace(parsed.Streams[0].Tags["language"]); lang != "" && lang != "und" {
			res.tags["language"] = lang
		}
	}
	return res, nil
}

func lowerTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}
