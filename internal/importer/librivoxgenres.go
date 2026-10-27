package importer

import "github.com/kodestar/audiosilo-meta/pkg/model"

// librivoxGenres is pkg/model's LibriVox genre-name table (see
// model.LibrivoxGenres for what it maps and what it leaves out on purpose),
// keyed lowercased as genreTable.lookupName keys names.
var librivoxGenres = model.LibrivoxGenres()

// librivoxGenreTable is the genre table a LibriVox run maps its claims through:
// names only (LibriVox states no node ids or ladders), with a run memo.
func librivoxGenreTable() genreTable {
	return genreTable{ByName: librivoxGenres}.withRunMemo()
}
