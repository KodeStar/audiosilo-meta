// Package query is the read side of the AudioSilo Meta release artifact. It
// opens the SQLite database internal/build compiles (Open), answers every
// question the JSON API asks of it, and serves that API (NewHandler) - the very
// handler meta.audiosilo.app runs, so a consumer that mounts it in-process (a
// mirror-mode AudioSilo server) answers byte for byte what the public service
// does, redirects, match scores and all.
//
// It is a PUBLIC API, and two rules follow from that:
//
//   - The handler's responses are a contract. Mirror-mode servers decode them
//     with the same structs they use against the remote service (CROSS-REPO
//     section 17 in the AudioSilo workspace), so a response shape changes the
//     way any public API's does: additively.
//   - Artifact schema changes must stay ADDITIVE - new tables and columns, never
//     a renamed or dropped one. A mirror-mode server downloads whatever the
//     newest data release is, so it routinely reads an artifact NEWER than its
//     own code: Open accepts one (every optional read is gated with >=, and Info
//     reports the version against MaxSchemaVersion), and a query that breaks on
//     it is a 5xx the server answers from the remote service instead.
//
// metaserve's own concerns - the hot swap, the HTML entity and guide pages, the
// sitemaps, the release poller and webhook, CORS and gzip - stay in
// internal/serve, which serves this package's handler for its API routes. The
// other exported reads and helpers are the ones metaserve's pages use; the
// response types they return are the API's JSON shapes.
package query
