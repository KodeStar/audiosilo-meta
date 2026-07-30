-- Export libex books as NDJSON rows in the shape `metaimport libex` parses.
-- Run: psql -d libex -tA -f libex-export-rows.sql > rows.ndjson
-- Ordered by sku_group so regional editions of one title are adjacent
-- (the importer folds them into one recording via the same-narrator ASIN merge).
-- Excludes AI-narrated (is_vvab) rows and everything that is not a book
-- (content_delivery_type SinglePartBook/MultiPartBook keeps 1.06M of 1.13M rows;
-- podcasts, periodicals, parts, bundles and series containers are out).
-- is_vvab is nullable, and NOT NULL is NULL, not TRUE - so `IS NOT TRUE` is what
-- keeps a row whose AI-narration flag was never recorded.
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
ORDER BY COALESCE(b.sku_group, b.asin), b.region, b.asin;
