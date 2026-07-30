-- Export libex books as NDJSON rows in the shape `metaimport libex` parses.
-- Run: psql -d libex -tA -f libex-export-rows.sql > rows.ndjson
-- Ordered by sku_group so regional editions of one title are adjacent
-- (the importer folds them into one recording via the same-narrator ASIN merge).
-- Excludes everything that is not a book (content_delivery_type
-- SinglePartBook/MultiPartBook keeps 1.06M of 1.13M rows; podcasts, periodicals,
-- parts, bundles and series containers are out).
--
-- AI-NARRATED ROWS ARE EXCLUDED TWICE, and the second filter is the one that
-- works. `is_vvab` ("virtual voice") looks like the flag for this, and it is
-- kept - but it does not do the job: measured over the full 1.13M-row dump,
-- 145,558 books credit an AI voice and is_vvab is FALSE on 145,550 of them and
-- true on only 8. The evidence lives in the NARRATOR CREDIT, so the credit is
-- what we filter on. (is_vvab is nullable in principle, and NOT NULL is NULL
-- rather than TRUE, so `IS NOT TRUE` is what would keep a row whose flag was
-- never recorded.)
--
-- The credit patterns below are evidence-driven, measured over that dump, and
-- are the SQL half of a pair: internal/importer/libex.go carries the same three
-- shapes (aiNarratorNames / aiNarratorPrefix / aiNarratorMarkers) and refuses
-- the same rows at parse time, so a dump exported without this filter is still
-- safe. KEEP THE TWO LISTS IN STEP - if you add a form here, add it there.
-- Deliberately NOT used: a substring search for "tts", or a bare "ai"/"ki"/"ia"
-- token. Those match Watts, Pitts, Ricketts, Ki Hong Lee and Ai-jen Poo, who
-- are real narrators.
-- books.isbn is deliberately NOT exported: it is sometimes the PRINT edition's
-- ISBN (verified against live records), and recording.isbn is a hard dedup key
-- upstream - a wrong one would mechanically refuse legitimate future
-- narrations. Same omit-never-guess call the site's /add prefill makes.
SELECT json_build_object(
  'asin', b.asin,
  'title', b.title,
  'subtitle', b.subtitle,
  'region', b.region,
  'publisher', b.publisher,
  'language', b.language,
  'bookFormat', b.book_format,
  'releaseDate', b.release_date,
  'imageUrl', b.image,
  'lengthMinutes', b.length_minutes,
  'authors', COALESCE((SELECT json_agg(json_build_object('name', a.name))
      FROM author_book ab JOIN authors a ON a.id = ab.author_id
      WHERE ab.book_asin = b.asin), '[]'::json),
  'narrators', COALESCE((SELECT json_agg(json_build_object('name', bn.narrator_name))
      FROM book_narrator bn WHERE bn.book_asin = b.asin), '[]'::json),
  'genres', COALESCE((SELECT json_agg(json_build_object('asin', g.asin, 'name', g.name, 'type', g.type))
      FROM book_genre bg JOIN genres g ON g.asin = bg.genre_asin
      WHERE bg.book_asin = b.asin), '[]'::json),
  'series', COALESCE((SELECT json_agg(json_build_object('name', s.title, 'position', bs.position))
      FROM book_series bs JOIN series s ON s.asin = bs.series_asin
      WHERE bs.book_asin = b.asin), '[]'::json),
  'chapters', (SELECT t.chapters FROM tracks t WHERE t.asin = b.asin LIMIT 1)
)
FROM books b
WHERE b.is_vvab IS NOT TRUE
  AND b.content_delivery_type IN ('SinglePartBook', 'MultiPartBook')
  -- ANY AI credit disqualifies the row (the Go side matches: see
  -- firstAINarrator). No book in the dump credits a human alongside a synthetic
  -- voice, so this decides nothing differently today - it just fails safe if one
  -- ever appears.
  AND NOT EXISTS (
    SELECT 1 FROM book_narrator bn
    WHERE bn.book_asin = b.asin
      AND (
        -- 1. Credits that are WHOLLY an AI-voice label, per localization.
        lower(btrim(bn.narrator_name)) IN (
          'virtual voice',    -- 143,559 credits, English
          'voz virtual',      -- 281, Spanish
          'voix virtuelle',   -- 171, French
          'voce virtuale',    -- 134, Italian
          'voz sintética',    -- 10, Spanish "synthetic voice"
          'voce artificiale', -- 2, Italian "artificial voice"
          'virtuelle stimme', -- 1, German
          'voz virual',       -- 1, a typo the dump really carries
          'digital voices')   -- 1, Loudly (an AI audio publisher)
        -- 2. Audible's "AI Voice <persona>" family (236 names, 1,139 credits),
        --    matched as a WORD prefix so a real "Ai Voicu" would not match.
        OR bn.narrator_name ~* '^ai voice($|[^[:alnum:]])'
        -- 3. A trailing parenthetical marker declaring the credit synthetic.
        --    Whole-marker match: the same position also carries human
        --    qualifiers like "(Skyboat Media)" and "(The Captain's Voice)".
        OR lower(substring(bn.narrator_name from '[(\[]([^()\[\]]*)[)\]][[:space:]]*$')) IN (
          'voz de ia',                 -- 500 credits, 33 names, Spanish
          'ai',                        -- 7
          'réplica de voz autorizada', -- 3, an authorized synthetic replica
          'authorized voice replica',  -- 2, the same in English
          'virtual voice',             -- 2
          'ki sprecher',               -- 1, German
          'kokoro tts')                -- 1, a named TTS engine
      ))
ORDER BY COALESCE(b.sku_group, b.asin), b.region, b.asin;
