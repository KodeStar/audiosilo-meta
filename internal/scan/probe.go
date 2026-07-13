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

// probe runs ffprobe against one file for duration, chapter count, and container
// tags. Best-effort: any failure returns (nil, err) and the caller degrades.
func probe(path, ffprobePath string) (*probeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_chapters",
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
	return res, nil
}

func lowerTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}
