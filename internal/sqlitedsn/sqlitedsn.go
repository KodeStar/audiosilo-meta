// Package sqlitedsn builds the `file:` DSN a SQLite handle is opened with. It is
// a LEAF - no dependency on anything else here - because two unrelated packages
// open an operator-supplied artifact path read-only (internal/serve's snapshot
// loader and internal/issueform's works-artifact stand-in) and a second spelling
// of this rule is a second chance to lose the read-only guarantee silently.
// metaserve's intake queue, the one database anything here writes while serving,
// opens its operator-supplied path through the same rule (ReadWrite).
package sqlitedsn

import (
	"net/url"
	"path/filepath"
)

// ReadOnly builds the read-only `file:` URI for an artifact path.
//
// The path is URI-ENCODED rather than spliced, which is the whole reason this is
// a function. An artifact path is operator-supplied (`--db`, `--works-db`, a
// cache directory) and a file URI is not a string with a path in it: a '?'
// anywhere in the path starts the QUERY, so `/tmp/a?b/meta.sqlite` names the
// file `/tmp/a` with the parameter `b/meta.sqlite` and DROPS mode=ro - a
// read-only guarantee lost silently, on a path the operator believes they named,
// and with it the guarantee that the file is not CREATED; a '#' starts a
// fragment and truncates just as quietly; and a literal '%2F' in a directory
// name is DECODED to '/' and opens a different file entirely. SQLite's URI
// parser does all three - they are its documented syntax, not a driver quirk -
// so the escaping has to happen before the string is handed over.
// url.URL.EscapedPath applies exactly the encodePath rule those three characters
// need.
//
// The path is made ABSOLUTE first, and that is load-bearing rather than tidy: a
// file URI with an empty authority needs a rooted path, and url.URL.String()
// renders a relative one as `file://meta.sqlite`, where the parser reads
// `meta.sqlite` as the HOST and finds no file at all.
func ReadOnly(path string) (string, error) {
	return fileURI(path, "mode=ro")
}

// ReadWrite builds the `file:` URI for a database the caller writes, created when
// absent, with each pragma applied to every connection the handle opens. The path
// is escaped and made absolute for the reasons on ReadOnly. A pragma is the
// driver's `_pragma` value, for example "journal_mode(DELETE)".
func ReadWrite(path string, pragmas ...string) (string, error) {
	query := "mode=rwc"
	for _, p := range pragmas {
		query += "&_pragma=" + url.QueryEscape(p)
	}
	return fileURI(path, query)
}

// fileURI is the one spelling of the rule on ReadOnly: absolute, then escaped.
func fileURI(path, query string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: query}
	return u.String(), nil
}
