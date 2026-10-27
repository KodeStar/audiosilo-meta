package importer

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// librivoxLanguages is pkg/model's LibriVox language-name table (see
// model.LibrivoxLanguages for what it holds and why): names LibriVox states that
// the shared word table does not, each mapped to a schema language tag. It lives
// in pkg/model so the API's LibriVox-shaped feed reads the very table the
// importer writes through.
var librivoxLanguages = model.LibrivoxLanguages()

// librivoxLanguage resolves a LibriVox language name (case-insensitive): this
// table first, then the shared word table. ok is false for a name neither holds.
func librivoxLanguage(name string) (string, bool) {
	if code, ok := librivoxLanguages[strings.ToLower(strings.TrimSpace(name))]; ok {
		return code, true
	}
	return mapLanguage(name)
}
