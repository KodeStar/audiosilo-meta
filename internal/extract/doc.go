// Package extract supports the Phase 3 epub -> characters/recaps extraction
// pipeline. It has two independent halves:
//
//   - Split turns an epub into one plain-text file per spine document (in spine
//     order) plus a manifest.json describing the chapter list. The chapter list
//     drives the spoiler position model authors key characters/recaps against.
//   - NGram mechanically checks authored sidecar JSON (characters/recaps) for
//     near-verbatim overlap with the source text, enforcing the no-verbatim
//     copyright rule documented in AUTHORING.md.
//
// Neither half writes into data/; the tool is an authoring aid whose outputs
// (chapter text, overlap findings) inform a human contributor.
package extract
