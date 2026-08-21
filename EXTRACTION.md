# Extracting characters and recaps from a book you own - moved

This guide now lives in the community repository, beside the layer it produces:

**[KodeStar/audiosilo-meta-community/EXTRACTION.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/EXTRACTION.md)**

It documents the agent process - rolling fact pass, notes-only synthesis,
adversarial spoiler audit - that turns a book you own into a characters/recaps
sidecar. Its output is **CC BY-SA 4.0** content, which since the community-repo
split (2026-08-21) is contributed to
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community)
rather than here. Read
[AUTHORING.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/AUTHORING.md)
there first; for an audiobook with no EPUB, use
[EXTRACTION-AUDIO.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/EXTRACTION-AUDIO.md).

The `metaextract` **tool** stays here (`cmd/metaextract`: `split` for EPUB ->
chapter text + manifest, `ngram` for the no-verbatim check), with its CLI usage
in the command's own doc comment. Source material and transcripts never enter
either repository; only the derived sidecars are committed.
