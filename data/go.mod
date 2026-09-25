// This file is NOT code. It exists ONLY to keep data/ (the catalogue's
// ~1.6 GB of JSON packs) out of the github.com/kodestar/audiosilo-meta module
// zip: the go command omits every subdirectory holding its own go.mod, and
// without it the zip passes the 500 MiB module ceiling, so a consumer of pkg/*
// (audiosilo-sidecars) cannot `go get` any release. data/ holds no Go files and
// nothing imports this module; its go line is inert and need not follow the
// root's. Deleting this file breaks every future tag for importers - see
// module_test.go at the repository root.
module github.com/kodestar/audiosilo-meta/data

go 1.26.0
