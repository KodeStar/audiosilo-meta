# Extracting characters and recaps from an audiobook you own - moved

This guide now lives in the community repository, beside the layer it produces:

**[KodeStar/audiosilo-meta-community/EXTRACTION-AUDIO.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/EXTRACTION-AUDIO.md)**

It is the audio-only sibling of
[EXTRACTION.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/EXTRACTION.md):
chapter-marker inspection, local chapter-isolated ASR, transcript quality checks
and proper-noun verification, feeding the same rolling fact pass, notes-only
synthesis boundary and independent audits. Its output is **CC BY-SA 4.0**
content, which since the community-repo split (2026-08-21) is contributed to
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community)
rather than here.

Audio orchestration is documented rather than a `metaextract` subcommand, so
nothing about the tool moved: `cmd/metaextract` stays in this repository, and
audio and transcripts never enter either one.
