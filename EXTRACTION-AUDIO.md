# Extracting characters and recaps from an audiobook you own

This guide is the audio-only sibling of [EXTRACTION.md](EXTRACTION.md).
Use it when you have an audiobook but no EPUB. Read [AUTHORING.md](AUTHORING.md)
first. Its position model, copyright rules, length caps, voice, and sidecar
schemas remain authoritative.

The audio path replaces EPUB splitting with chapter-marker inspection, local
speech recognition, transcript quality checks, and spelling verification. The
rest of the proven pipeline is unchanged:

```text
audiobook
  1. inspect       read metadata and embedded chapter markers
  2. normalize     map recording markers to logical work chapters
  3. transcribe    local, chapter-by-chapter ASR with timestamps
  4. crosscheck    review ASR quality and verify names and places
  5. fact pass     rolling chapter notes in own words
  6. synthesis     characters.json + recaps.json from the notes only
  7. verify        metafmt + metacheck + n-gram + independent audits
```

The load-bearing boundary is still between the fact pass and synthesis. The
synthesis stage sees only chapter-attributed notes, never audio or transcripts.
A transcript is an imperfect aid for hearing the source, not publication-ready
text or authoritative spelling.

## Ground rules

- Use an audiobook you lawfully have and follow the laws that apply to you.
  This guide does not cover obtaining or removing access controls from books.
- Keep the audiobook, extracted audio, raw and corrected transcripts, fact
  notes, spelling ledger, and model scratch files outside the repository. Only
  the final derived CC BY-SA sidecars may be committed.
- Do not distribute transcripts. A transcript can reproduce most of a book
  even when it contains recognition errors.
- Prefer local ASR. The validated path below does not upload the audiobook. Its
  first run downloads the model. A cloud ASR service sends source material to a
  third party and is outside this tested recipe.
- The CC0 core must exist first: work, recording, people, and series entry where
  applicable. Sidecars attach to the work.
- Keep logical chapter numbers edition-independent. Recording marker numbers
  and timestamps are private audit aids, not public sidecar positions.
- External references may verify identity and spelling only. Never use their
  plot prose as input to the fact pass or synthesis.

## Validated prerequisites

The tested Apple Silicon path uses:

- `ffmpeg` and `ffprobe`
- Python 3
- `mlx-whisper==0.4.3`
- `mlx-community/whisper-large-v3-turbo`

Create an isolated work area and environment. Substitute paths for the book and
working directory throughout this guide.

```sh
export BOOK="/path/to/book.m4b"
export WORK="/tmp/audiosilo-audio-extract"
mkdir -p "$WORK"
python3 -m venv "$WORK/.venv"
"$WORK/.venv/bin/pip" install 'mlx-whisper==0.4.3'
```

The model used for the worked example resolved to Hugging Face snapshot
`a4aaeec0636e6fef84abdcbe3544cb2bf7e9f6fb`. Record the snapshot used by a real
run because a model repository can be updated. `large-v3-turbo` was selected
for accuracy, especially around unusual names, rather than minimum download or
runtime. The downloaded model was about 1.6 GB.

On non-Apple hardware, use a local Whisper implementation such as
`whisper.cpp` or `faster-whisper` with JSON output and timestamps. Keep the same
chapter isolation, spelling review, notes-only boundary, and audits. Performance
and command-line flags differ, so do not treat the benchmark below as portable.

Do not run concurrent ASR processes on one machine. Three concurrent
mlx-whisper jobs measured about 30 percent WORSE aggregate throughput than one,
from Metal contention (CPU idle percentage is misleading). The right parallelism
is pipelining: transcribe book N+1 while book N's model passes run.

## Step 1: inspect the recording

Capture metadata and all embedded markers before extracting audio:

```sh
ffprobe -v error -show_format -show_streams -show_chapters \
  -of json "$BOOK" > "$WORK/probe.json"
```

Review at least:

- title, author, narrator, series, and identifiers
- total duration and audio stream
- every marker's title, start, end, and duration
- opening credits, end credits, parts, interludes, and bonus material
- whether labels state logical chapter numbers

The marker list is a recording timeline, not automatically the work's position
model. For example, marker 1 may be opening credits while marker 2 is logical
chapter 1. Create a manifest that maps each selected interval to the work's
logical chapter. Validate that chapter numbers are unique, ordered, and
contiguous unless the work itself intentionally uses another scheme.
Only those logical numbers enter sidecars; marker indexes remain recording
details.

Never infer chapter numbers merely from marker count. If labels are ambiguous,
listen to each boundary and compare with an official table of contents or
another spelling-only source.

## Step 2: extract and transcribe by logical chapter

Chapter-local files provide three safeguards:

1. a crash can resume at the next unfinished chapter;
2. every transcript fact has a hard chapter boundary;
3. a suspicious phrase can be replayed from a short timestamped interval.

The following script is the tested orchestration pattern for a single M4B whose
logical markers are named `Chapter N` or `Chapter N: Title`. Save it as
`$WORK/audio_extract.py`. Review and adapt `chapter_from_marker()` before using
it on a differently labelled book.

```python
#!/usr/bin/env python3
import argparse
import json
import re
import subprocess
import sys
import time
from pathlib import Path


def command(*args: str, capture: bool = False) -> subprocess.CompletedProcess:
    return subprocess.run(
        args,
        check=True,
        capture_output=capture,
        text=capture,
    )


def chapter_from_marker(title: str):
    match = re.fullmatch(r"Chapter\s+(\d+)(?::\s*(.*))?", title, re.IGNORECASE)
    if not match:
        return None
    return int(match.group(1)), (match.group(2) or "").strip() or None


def transcript_is_complete(path: Path) -> bool:
    if not path.is_file():
        return False
    try:
        data = json.loads(path.read_text())
    except (OSError, ValueError):
        return False
    return isinstance(data.get("text"), str) and isinstance(data.get("segments"), list)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("audio", type=Path)
    parser.add_argument("work", type=Path)
    parser.add_argument("--language", default="en")
    parser.add_argument(
        "--model",
        default="mlx-community/whisper-large-v3-turbo",
    )
    parser.add_argument(
        "--prompt",
        default="",
        help="Verified title, author, and spellings only",
    )
    args = parser.parse_args()

    if not args.audio.is_file():
        sys.exit(f"missing input: {args.audio}")

    chapters_dir = args.work / "chapters"
    transcripts_dir = args.work / "transcripts-raw"
    chapters_dir.mkdir(parents=True, exist_ok=True)
    transcripts_dir.mkdir(parents=True, exist_ok=True)

    probe = command(
        "ffprobe",
        "-v", "error",
        "-show_format",
        "-show_chapters",
        "-of", "json",
        str(args.audio),
        capture=True,
    )
    media = json.loads(probe.stdout)

    selected = []
    for marker in media.get("chapters", []):
        marker_title = marker.get("tags", {}).get("title", "")
        parsed = chapter_from_marker(marker_title)
        if parsed is None:
            continue
        chapter, chapter_title = parsed
        start = float(marker["start_time"])
        end = float(marker["end_time"])
        selected.append({
            "chapter": chapter,
            "title": chapter_title,
            "marker_title": marker_title,
            "start": start,
            "end": end,
            "duration": end - start,
        })

    selected.sort(key=lambda item: item["start"])
    numbers = [item["chapter"] for item in selected]
    expected = list(range(1, len(selected) + 1))
    if not selected or numbers != expected:
        sys.exit(
            "logical chapter markers need manual mapping: "
            f"got {numbers}, expected {expected}"
        )

    manifest = {
        "source": str(args.audio),
        "title": media.get("format", {}).get("tags", {}).get("title"),
        "duration": float(media.get("format", {}).get("duration", 0)),
        "chapter_count": len(selected),
        "chapters": selected,
    }
    (args.work / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n"
    )

    for item in selected:
        number = item["chapter"]
        output = chapters_dir / f"ch{number:03d}.flac"
        if output.is_file():
            continue
        command(
            "ffmpeg",
            "-hide_banner", "-loglevel", "error", "-y",
            "-ss", f'{item["start"]:.3f}',
            "-i", str(args.audio),
            "-t", f'{item["duration"]:.3f}',
            "-map", "0:a:0", "-vn",
            "-ac", "1", "-ar", "16000", "-c:a", "flac",
            str(output),
        )

    whisper = args.work / ".venv" / "bin" / "mlx_whisper"
    started = time.monotonic()
    for item in selected:
        number = item["chapter"]
        audio = chapters_dir / f"ch{number:03d}.flac"
        output = transcripts_dir / f"ch{number:03d}.json"
        if transcript_is_complete(output):
            print(f"skipping completed chapter {number}", flush=True)
            continue
        if output.exists():
            output.unlink()
        print(f"transcribing chapter {number}/{len(selected)}", flush=True)
        call = [
            str(whisper), str(audio),
            "--model", args.model,
            "--language", args.language,
            "--output-dir", str(transcripts_dir),
            "--output-format", "json",
            "--word-timestamps", "True",
            "--verbose", "False",
        ]
        if args.prompt:
            call.extend(["--initial-prompt", args.prompt])
        command(*call)
        if not transcript_is_complete(output):
            sys.exit(f"incomplete transcript output: {output}")
        elapsed = (time.monotonic() - started) / 60
        print(f"completed chapter {number} after {elapsed:.1f} min", flush=True)


if __name__ == "__main__":
    main()
```

Run it with a short prompt containing only spellings already verified from
embedded metadata or front matter. Do not provide a full-book cast list:

```sh
"$WORK/.venv/bin/python" "$WORK/audio_extract.py" "$BOOK" "$WORK" \
  --prompt "<Title>. <Author>. <Narrators>. <Verified front-matter terms>."
```

Do not seed guesses. An initial prompt can make a wrong spelling recur more
consistently. The script skips complete JSON outputs and retries malformed or
partial outputs, so the same command is safe after interruption.

Preserve the raw JSON unchanged. It contains segment and word timestamps needed
for audit and selective listening. Generate separate plain-text and sanitized
JSON directories for later tools. MLX Whisper 0.4.3 can emit non-finite
`avg_logprob` values, represented as `NaN`, which strict JSON readers reject.
This extraction pattern accepts those values, converts them to `null` only in a
sanitized copy, and leaves the raw evidence intact:

```python
#!/usr/bin/env python3
import json
import os
from pathlib import Path

root = Path(os.environ["WORK"])
raw_dir = root / "transcripts-raw"
text_dir = root / "transcripts-text"
safe_dir = root / "transcripts-json"
text_dir.mkdir(exist_ok=True)
safe_dir.mkdir(exist_ok=True)

for source in sorted(raw_dir.glob("ch*.json")):
    data = json.loads(source.read_text(), parse_constant=lambda value: None)
    text = data.get("text", "").strip()
    (text_dir / f"{source.stem}.txt").write_text(text + "\n")
    (safe_dir / source.name).write_text(
        json.dumps(data, indent=2, allow_nan=False) + "\n"
    )
```

## Step 3: transcript QA

Do not start the fact pass merely because every output file exists. Check:

- one transcript for every manifest chapter and no extras
- non-empty text and plausible duration for every chapter
- the spoken chapter number and title against the marker label
- repeated phrases, long omissions, hallucinated text around silence, and
  abrupt starts or endings
- low-confidence words and segments as review candidates
- a sample from the start, middle, and end of every narrator's sections

Confidence is a triage signal, not proof. A confident model can spell a name
incorrectly or choose the wrong homophone. Conversely, a low-confidence word
may be correct. Keep a QA report listing the chapter, relative timestamp,
reason for review, action taken, and status.

### Degeneration sweep (required)

ASR degeneration is real, silent, and common: the model drops into a repeated
loop or collapses mid-chapter and emits no error. In the multi-book validation
below it appeared in every book, affecting roughly 2-4 percent of chapters, and
one instance had swallowed nearly half a chapter. Never skip this sweep.

1. Compute words per hour for every chapter and flag outliers against the book
   mean. Low outliers (more than ~2.5 standard deviations below the mean) mark
   lost content; high outliers matter too, because loop spam inflates the count.
2. Scan the raw JSON segments for runs of repeated or near-identical segments.
   Distinguish a benign chapter-END fade (a short line repeated at the fade-out,
   in the final stretch of the chapter) from a MID-chapter run (starting below
   ~85 percent of chapter position), which almost always means real narration
   was overwritten.
3. Retranscribe every flagged chapter fresh and adjudicate by word count and
   duration plausibility (roughly 140-150 words per minute of audio). NEVER
   blindly adopt the fresh run: a fresh run can degenerate identically, and one
   validated chapter kept its original after two re-runs collapsed at the same
   point.
4. For garble that survives full-chapter re-runs, extract the affected window
   as a clip (the command below), retranscribe just the clip, and splice the
   result into the corrected layer. This recovered every deterministic loop in
   the validation, including the one that had eaten half a chapter.

The chapter-end repeated-line fade is a distinct, recognizable benign class: a
short line repeated at the fade-out, sometimes garbled in the later repeats. The
FIRST occurrence is authentic unless the corruption begins inside it. Tell the
fact-pass prompts about it explicitly so they do not treat it as content.

When a passage is suspect, extract a short clip using the manifest's absolute
chapter start plus the transcript's relative timestamp, listen to it, and if
useful retranscribe that clip with the strongest available model and a prompt
containing only verified spellings. Do not replace the raw transcript. Record a
correction in the working layer.

For example, this extracts 25 seconds beginning 3:10 into logical chapter 12:

```sh
ffmpeg -hide_banner -loglevel error -ss 00:03:10 \
  -i "$WORK/chapters/ch012.flac" -t 25 "$WORK/review-ch012-0310.flac"
```

## Step 4: verify names, places, and terminology

Names and invented places are the principal audio-only risk. Pronunciation often
cannot determine whether a name uses `C` or `K`, contains a silent letter, or
has a space, apostrophe, or diacritic. Treat all raw ASR spellings as candidates.

Maintain a private `spellings.md` or structured ledger with:

- canonical spelling
- observed ASR variants
- entity type, such as person, place, group, or ability
- source URL or local metadata source
- chapter and relative timestamp of first use
- status: `verified`, `probable`, or `unresolved`
- notes about conflicts and the decision

Use this evidence order:

1. embedded audiobook metadata and exact chapter labels
2. official author, publisher, or series material
3. the book's public catalogue records or official table of contents
4. book-scoped wiki page titles or structured navigation
5. agreement among multiple independent public references
6. manual listening and selective retranscription

A wiki is a discovery source, not automatic authority. Different pages can
conflict, and page titles can contain aliases or mistakes. Querying a MediaWiki
API for links and categories can provide candidate spellings without copying
plot prose. Verify each spelling independently before changing the working
transcript.

Useful discovery techniques include:

- collect capitalized or repeatedly occurring transcript phrases
- compare candidate phrases with official title lists using normalized and
  fuzzy matching
- inspect all low-confidence occurrences of a candidate
- search for variants across chapters before deciding they are one entity
- check first occurrence at its timestamp against the audio

For web serials and their fan wikis, these tactics proved reliable at scale:

- A fandom wiki that returns a 402 to a direct fetch is often reachable through
  a text-rendering proxy or its MediaWiki API, neither of which copies plot
  prose.
- Chapter-title URL slugs on the serial platform (for example a Royal Road
  `chapter-39-crimsonwolf` slug) are strong written evidence for a name.
- For a serial, the author's own published chapter text, even seen only through
  a search-result snippet, is tier-2 official spelling.
- A stable global-chapter = offset + book-relative-chapter mapping lets a marker
  title be cross-verified against its published slug.

Warning: a search engine's AI summary can fabricate page content that was never
on the page. Trust only a fetched page body or a literal quoted snippet, never a
generated summary.

Apply verified corrections to a separate `transcripts-corrected` text directory
or through an explicit correction map. Never silently edit raw JSON. Do not
apply a global replacement where a common word and a proper name collide.

External references establish orthography and identity only. They must not add
an event, relationship, character status, reveal, or any other plot fact. Mark
conflicts unresolved until stronger evidence exists. Omit an unresolved
spelling from the published sidecars unless an independently verified alias is
safe and sufficient.

Keep full-book spelling discovery separate from the rolling fact pass. For a
chunk ending at chapter N, generate `spellings-through-ch<N>.md` containing
only verified terms whose first heard use is at or before chapter N. A future
character name can otherwise bias an earlier ambiguous transcript and break
the spoiler boundary.

## Step 5: run the rolling fact pass

After chapter and spelling QA, use the rolling fact pass from
[EXTRACTION.md](EXTRACTION.md). Replace its EPUB chapter paths with the corrected
chapter text paths. Keep raw JSON available only for timestamped audit.

Chunk the fact pass by word budget, not a fixed chapter count. EXTRACTION.md's
8-9 chapters assumes ~30-40k-word chunks; short-chaptered books need 12-14
chapters to reach the same ~20-25k words per chunk. Size each chunk to the word
budget so chapter attribution and sequential latency both stay sensible.

Add these audio-specific instructions to each fact-pass prompt:

```text
The chapter input is an ASR transcript and can contain omissions, homophones,
false punctuation, and incorrect proper nouns. Treat the provided
spellings-through-ch<N>.md as canonical only for entries marked verified. It
must not contain terms first heard after this chunk. Never repair a probable or
unresolved term by guessing. If an unclear word affects a material fact, write
NEEDS AUDIO REVIEW with the chapter and relative timestamp instead of asserting
the fact.

For every material bullet, retain a private audit citation in the form
[ch<N> @ MM:SS-MM:SS]. Citations remain in fact notes only and never enter the
published sidecars. Write all factual notes in fresh words. Do not copy
transcript sentences or dialogue.
```

Process chunks sequentially. A later chunk receives the previous cumulative
knowledge sheet but does not receive or reread earlier transcripts. This keeps
chapter attribution auditable. Resolve every material `NEEDS AUDIO REVIEW`
entry before synthesis or omit the affected fact.

## Step 6: synthesize from notes only

Use the synthesis prompt in [EXTRACTION.md](EXTRACTION.md), but apply recap
frequency from [AUTHORING.md](AUTHORING.md): add through-points according to
length and density, normally every 5-10 logical chapters or 2-4 listening hours
at natural breaks. Long or dense books may need many more than 6-8 points.

The synthesis stage receives only:

- the authoritative authoring contract
- private fact and cumulative knowledge notes
- verified canonical spellings referenced by those notes, without source prose

It does not receive audio, transcripts, wiki pages, or catalogue descriptions.
Timestamps stay in private notes. Final `reveal.chapter` and `through.chapter`
values use the logical chapter manifest.

### Publishing partly-verified names

The verified-only gate from step 4 has a few sanctioned relaxations, learned at
scale:

- A `probable` name that is an ordinary English word (Beyond, Frost, Sparrow)
  carries no orthographic risk and may be published as heard.
- An invented `probable` name corroborated by a wiki page title may be
  sanctioned case by case, with an explicit ledger note recording the decision.
- Everything else still unverified is rephrased generically or anonymized (for
  example "the team's warder"). The validation showed this loses nothing a
  reader would miss.
- For a genuinely unnameable recurring figure, an anonymized plain-English card
  ("The Team's Warder", "The Disciple") is the established pattern.

## Step 7: verify

Run the standard mechanical checks from the repository root:

```sh
go run ./cmd/metafmt --write
go run ./cmd/metacheck
go run ./cmd/metaextract ngram --source "$WORK/transcripts-text" \
  data/works/<shard>/<slug>/characters.json \
  data/works/<shard>/<slug>/recaps.json
```

If corrections were made, repeat the check against the corrected text:

```sh
go run ./cmd/metaextract ngram --source "$WORK/transcripts-corrected" \
  data/works/<shard>/<slug>/characters.json \
  data/works/<shard>/<slug>/recaps.json
```

The n-gram tool builds shingles independently for each `.txt` file, so no match
crosses chapter boundaries. Run it against both raw text and corrected text if
corrections were made.

This check is necessary but weaker than the EPUB check. ASR can alter one word
inside an otherwise copied eight-word sequence and create a false negative. A
clean result does not prove original phrasing. Preserve the notes-only synthesis
boundary and add an independent prose audit that compares suspicious final
phrasing with cited transcript intervals. Rewrite any close paraphrase in
fresh, concise reference-guide language.

A fresh independent session must also perform the spoiler audit from
[EXTRACTION.md](EXTRACTION.md), plus an audio-specific audit:

- every published name, place, group, and invented term is `verified`
- every material note can be checked at its chapter-relative timestamp
- no unresolved ASR interpretation became a published fact
- all public positions are logical chapters, not recording marker indexes
- marker offsets and narrator changes did not move facts across chapters

Expect the first synthesis to fail. In the multi-book validation every
first-draft synthesis failed its adversarial audit with at least one genuine
defect, most often a character card folding later-chapter abilities or twists
into an early reveal - cards drift toward whole-book summaries, while recaps
gate more reliably. Budget one fix then re-verify round (about 25 minutes) per
book as the norm, not the exception. The n-gram check also fired on genuinely
notes-only output twice, on factual-listing phrasing, so it is not redundant
with the notes-only boundary.

## Missing or unreliable markers

Do not claim chapter-accurate positions until boundaries are defensible.

- **Separate chapter files:** map each file to a logical chapter and build the
  same manifest. Do not assume filename order when tags disagree.
- **Credits and parts mixed with chapters:** exclude credits; retain parts as
  structural notes but do not count them as chapters unless the work does.
- **One marker contains several chapters:** listen for announced headings and
  record manual boundaries before ASR. Store the reasoning in the manifest.
- **No markers:** transcribing one long file may help locate spoken headings,
  but ASR headings are candidate boundaries only. Confirm them by listening and
  against an authoritative chapter list.
- **No spoken headings and no trustworthy chapter list:** whole-book summaries
  may still be researched privately, but spoiler-tagged chapter positions are
  not supportable. Stop rather than invent precision.
- **Abridged recording:** do not assume it can support sidecars for the
  unabridged work. Missing scenes can make facts and positions incomplete.
- **Multiple narrators:** sample every narrator and speaking style. A model that
  performs well on one narrator may fail on another.

## Series runs

Extracting a whole series adds cross-book carryover on top of the per-book
pipeline.

- **Seed the next book with the last one's knowledge.** Book N's first
  fact-pass chunk receives book N-1's `knowledge-final.md` as its starting
  sheet. Characters already known from earlier books are not re-INTRODUCED, and
  the book-N "previously" recap gets real grounding instead of a guess.
- **Carry the spelling ledger forward.** ASR re-mishears the same names in every
  book, and one name often has different variants across books. Apply the
  accumulated correction list to each new book's corrected layer.
- **Prior-book knowledge is spoiler-safe; external references are not plot.**
  Facts established in earlier books are always safe to use in a later book's
  cards and recaps. External references still contribute spelling only, even
  across books. In one validated case a wiki stated a character's parent was the
  father while the book's own text said the mother - the book wins, because
  wikis contribute orthography, never plot facts.

## Worked example: Silvers

This process was validated on *Silvers - Quest Academy, Book 1* by Brian J.
Nordon, narrated by Daniel Wisniewski and Rebecca Woods:

- source: one roughly 1.1 GB M4B, duration 20:00:17.649
- markers: 88 total, comprising opening credits, 86 logical chapters, and end
  credits
- labels: all 86 logical markers matched `Chapter N: Title` and formed a
  contiguous 1-86 position model
- environment: Mac Studio, Apple M1 Ultra, 64 GB RAM, FFmpeg 7.1.1,
  `mlx-whisper` 0.4.3, MLX 0.32.0
- model: `mlx-community/whisper-large-v3-turbo`
- chapter 1: 16:05 of audio transcribed in 33.7 seconds
- remaining 85 chapters: 44.9 minutes after chapter 1 had already completed
- effective total ASR time: about 45.5 minutes for 20 hours of audio, excluding
  model download and audio extraction
- output: 86 JSON transcripts, 206,333 text tokens by the QA tokenizer, and
  1,141,247 text characters
- heading QA: 85 of 86 spoken headings matched their embedded marker; ASR
  rendered the remaining title `Stakes` as the homophone `Steaks`, while the
  marker supplied the authoritative title
- word-confidence QA: mean reported word probability 0.986; 1,594 of 206,393
  timestamped words, about 0.77 percent, were below 0.5 and became review
  candidates
- JSON QA: MLX emitted 981 non-finite segment `avg_logprob` values across 23
  chapters, confirming the need for raw preservation and sanitized derivatives
- spelling discovery: a book-scoped MediaWiki link list supplied 65 candidate
  titles; exact variants appeared in 52 transcripts, while fuzzy comparison
  surfaced useful review candidates such as `Blathnid Clean` versus
  `Blathnaid Clean`, `Anthony McGinn` versus `Anthony McGuinn`, and
  `Con LeFleur` versus `Con LaFleur`

The 52 of 65 exact-title result is not an accuracy score. Several titles are
ordinary single words, while a correct entity may appear only under an alias.
It only demonstrates that structured title lists and fuzzy matching efficiently
produce a human review queue. The same public index also exposed conflicting
`Blathnaid Clean` and `Blathnaid McClean` forms, demonstrating why no wiki
correction should be applied blindly.

The run established feasibility: chapter-local ASR produced usable prose
far faster than real time, but heading homophones, unusual-name variants,
source conflicts, and non-finite confidence fields all required the review
layers above.

The process was subsequently run at scale: 11 books across two series, about
239 hours of audio, with every sidecar passing an independent adversarial audit.
On the same hardware the per-book steady state is roughly ASR 45-60 minutes
(pipelined away behind the previous book), fact pass 60-75 minutes, synthesis
about 10 minutes, and the audit loop about 25 minutes.

## Final checklist

- [ ] Source audio and all intermediate artifacts are outside Git.
- [ ] The manifest excludes credits, samples, and unrelated bonus material.
- [ ] Every selected interval maps to one defensible logical work chapter.
- [ ] Every chapter has complete raw JSON, text, and timestamps.
- [ ] Raw transcripts remain immutable; corrections are separate and auditable.
- [ ] Transcript QA covers omissions, repetition, silence, boundaries, and all
      narrators.
- [ ] The required degeneration sweep retranscribed every word-per-hour and
      mid-chapter-loop outlier; recovered loops were clipped and spliced into
      the corrected layer.
- [ ] Every published proper noun is verified; conflicts are resolved or
      omitted.
- [ ] External references contributed spelling only, never plot facts or prose.
- [ ] The fact pass is sequential, chapter-attributed, and written in own words.
- [ ] Synthesis saw notes only, never source audio or transcripts.
- [ ] Recap frequency follows AUTHORING.md's length and density guidance.
- [ ] N-gram checks pass against raw and, when present, corrected transcript
  text.
- [ ] Independent originality, spoiler, spelling, and timestamp audits pass.
- [ ] `metafmt`, `metacheck`, and the repository gate pass.
- [ ] Audio, transcripts, notes, ledger, and scratch files are deleted when no
      longer needed and are never committed.
