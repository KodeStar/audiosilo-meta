# Authoring characters and recaps - moved

This guide now lives in the community repository, beside the layer it is about:

**[KodeStar/audiosilo-meta-community/AUTHORING.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/AUTHORING.md)**

Characters and recaps are the **CC BY-SA 4.0** layer of the database, and since
the community-repo split (2026-08-21) they live in
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community) -
data, issue forms, intake and authoring guides together. This repository is the
**CC0 core**: works, recordings, people, series.

This stub is deliberately not a copy. The guide walks a contributor through
adding a sidecar to `data/works-community/`, and that path does not exist here -
following the old copy would have produced a pull request this repository's CI
refuses by design (`--profile core` reports a `works-community/` file as an
unrecognized location).

**Where to go instead:**

| If you want to | Go to |
|---|---|
| Author characters or recaps by hand or with an agent | [AUTHORING.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/AUTHORING.md) in the community repo |
| Extract them from a book you own | [EXTRACTION.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/EXTRACTION.md) |
| Extract them from an audiobook you own | [EXTRACTION-AUDIO.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/EXTRACTION-AUDIO.md) |
| Submit one | [The community repo's issue forms](https://github.com/KodeStar/audiosilo-meta-community/issues/new/choose) |
| Contribute a factual record (work, recording, person, series) | [CONTRIBUTING.md](CONTRIBUTING.md), here |

The `metaextract` **tool** stays in this repository with the rest of the tooling
(`cmd/metaextract` - EPUB splitting and the n-gram no-verbatim check); its CLI
usage is in the command's own doc comment. Only the process documentation moved.
