# Security Policy

## Reporting a vulnerability

Email **kode@audiosilo.app** with the details. Please do not open a public issue
for a security problem.

Include what you found, how to reproduce it, and the potential impact. We will
acknowledge the report, investigate, and keep you informed of the fix. Please
give us a reasonable window to address the issue before any public disclosure.

## In scope

- **CI workflows** (`.github/workflows/`) - especially anything that could let a
  fork pull request run privileged code or exfiltrate secrets.
- **The Go tooling** (`cmd/`, `internal/`) - `metacheck`, `metafmt`, `metabuild`
  (for example, path traversal or resource exhaustion when processing untrusted
  data files).
- **The future API server** (planned, Phase 1) once it exists.

## Out of scope

The metadata content itself is public, community-editable factual data - errors
or disputes in the data are not security issues. Use a **Correct data** issue or
a pull request for those, and see [LICENSING.md](LICENSING.md) for the takedown /
rightsholder opt-out channel.
