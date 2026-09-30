-- Export EVERY row naming an ASIN in a comma-separated psql variable.
-- psql -X -tA -v ON_ERROR_STOP=1 -v asins="B000000001,B000000002" \
--   -f scripts/libex-relocate-rows.sql > relocate.ndjson
-- Keep the SELECT in step with libex-export-rows.sql. No credit/content filters:
-- relocation must see contradictory copies too; Go applies the admission rules.
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
  -- tracks.chapters is an OBJECT wrapping the list ({"chapters": [...]}), so the
  -- list is unwrapped here. The importer's chapter reader type-asserts an ARRAY
  -- and returns nil for anything else (rawBook.chapters), so exporting the
  -- wrapper drops every chapter SILENTLY - no warning, no failed row, just books
  -- that arrive with no chapters at all.
  'chapters', (SELECT t.chapters->'chapters' FROM tracks t WHERE t.asin = b.asin LIMIT 1)
)
FROM books b
WHERE b.asin = ANY(string_to_array(:'asins', ','))
ORDER BY b.asin, b.region;
