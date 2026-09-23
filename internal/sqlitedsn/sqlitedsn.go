// Package sqlitedsn builds the `file:` DSN a read-only SQLite handle is opened
// with. It is a LEAF - no dependency on anything else here - because two
// unrelated packages open an operator-supplied artifact path read-only
// (internal/serve's snapshot loader and internal/issueform's works-artifact
// stand-in) and a second spelling of this rule is a second chance to lose the
// read-only guarantee silently.
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
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro"}
	return u.String(), nil
}
