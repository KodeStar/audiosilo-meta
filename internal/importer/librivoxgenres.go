package importer

// librivoxGenres maps LibriVox's genre names (lowercased, as genreTable.lookupName
// keys them) onto this project's genre vocabulary - LICENSING.md's "never a
// retailer's taxonomy verbatim", which holds for a public-domain catalogue's
// taxonomy too. Only a genre with an evident vocabulary counterpart is mapped.
// Left out on purpose, and reported once per run as unmapped:
//
//   - LibriVox's PERIOD genres ("Published before 1800", "Published 1800 -1900",
//     "Published 1900 onward"): a publication date is not a genre;
//   - its umbrella and format genres ("General Fiction", "Fantastic Fiction",
//     "Dramatic Readings", "Anthologies", "Single Author Collections"), which
//     name no single vocabulary entry;
//   - anything not listed here, which a backfill tranche's unmapped line
//     surfaces and a later change adds deliberately.
var librivoxGenres = map[string]string{
	"action & adventure fiction":         "action-adventure",
	"biography & autobiography":          "biography-memoir",
	"children's fiction":                 "childrens",
	"children's non-fiction":             "childrens",
	"classics (greek & latin antiquity)": "classics",
	"cooking":                            "food-cooking",
	"crime & mystery fiction":            "mystery",
	"detective fiction":                  "mystery",
	"economics":                          "economics",
	"education":                          "education",
	"erotica":                            "erotica",
	"essays & short works":               "essays",
	"fairy tales":                        "fairy-tales-folklore",
	"fantasy fiction":                    "fantasy",
	"ghost stories":                      "ghost-stories",
	"gothic fiction":                     "gothic",
	"historical fiction":                 "historical-fiction",
	"history":                            "history",
	"horror & supernatural fiction":      "horror",
	"humorous fiction":                   "comedy-humor",
	"language learning":                  "language",
	"law":                                "law",
	"literary fiction":                   "literary-fiction",
	"medical":                            "medicine",
	"memoirs":                            "biography-memoir",
	"music":                              "music",
	"myths, legends & fairy tales":       "fairy-tales-folklore",
	"nature":                             "nature",
	"philosophy":                         "philosophy",
	"plays":                              "drama-plays",
	"poetry":                             "poetry",
	"political science":                  "politics",
	"psychology":                         "psychology",
	"religion":                           "religion-spirituality",
	"religious fiction":                  "religious-fiction",
	"romance":                            "romance",
	"satire":                             "comedy-humor",
	"science":                            "science",
	"science fiction":                    "science-fiction",
	"self-help":                          "self-help",
	"short stories":                      "short-stories",
	"travel & geography":                 "travel",
	"war & military fiction":             "war-military-fiction",
	"westerns":                           "westerns",
}

// librivoxGenreTable is the genre table a LibriVox run maps its claims through:
// names only (LibriVox states no node ids or ladders), with a run memo.
func librivoxGenreTable() genreTable {
	return genreTable{ByName: librivoxGenres}.withRunMemo()
}
