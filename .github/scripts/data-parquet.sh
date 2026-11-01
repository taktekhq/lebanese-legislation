#!/usr/bin/env bash
# Export a database loaded from data/ to the Hugging Face Parquet tables, one file per table.
#
#   .github/scripts/data-parquet.sh DATABASE_URL OUT_DIR
#
# The same tables and columns as the dataset on Hugging Face: every column but the
# search-only ones (tsvector) and the internal provenance columns, the views
# (graph_node, article_in_force, legislation_version, possibly_affected) as tables,
# legislation with its English title, and the calculated current amounts from
# data/article_calculated_current.jsonl. Needs psql and the duckdb CLI.
set -euo pipefail
DB="${1:?usage: data-parquet.sh DATABASE_URL OUT_DIR}"
OUT="${2:?usage: data-parquet.sh DATABASE_URL OUT_DIR}"
DATA="$(cd "$(dirname "$0")/../../data" && pwd)"
TABLES=(legislation legislation_title_en legislation_kind legislation_section legislation_article
  legislation_version amendment article_version article_in_force possibly_affected centre_error code
  ruling ruling_file ruling_page court opinion agreement doctrine_article research gazette_issue
  gazette_entry reference source_page legislation_scan legislation_alias date_correction graph_edge graph_node)
rm -rf "$OUT" && mkdir -p "$OUT"
for t in "${TABLES[@]}"; do
  cols=$(psql "$DB" -XAtq -c "SELECT string_agg(quote_ident(column_name), ', ' ORDER BY ordinal_position)
    FROM information_schema.columns WHERE table_schema = 'public' AND table_name = '$t' AND udt_name <> 'tsvector'
    AND NOT (table_name IN ('legislation_article', 'article_version') AND column_name IN ('source_kind', 'source_id'))")
  [ -n "$cols" ] || { echo "data-parquet: $t is not in the database" >&2; exit 1; }
  from="$cols FROM pg.public.$t"
  if [ "$t" = legislation ]; then
    from="$(echo "$cols" | sed -E 's/(^|, )/\1l./g'), te.title_en AS title_en FROM pg.public.legislation l
      LEFT JOIN pg.public.legislation_title_en te ON te.law_id = l.id"
  fi
  duckdb -c "INSTALL postgres; LOAD postgres; ATTACH '$DB' AS pg (TYPE postgres, READ_ONLY);
    COPY (SELECT $from) TO '$OUT/$t.parquet' (FORMAT parquet, COMPRESSION zstd, ROW_GROUP_SIZE 100000);" >/dev/null
  echo "$t: $(du -h "$OUT/$t.parquet" | cut -f1)"
done
duckdb -c "COPY (
  SELECT law_id, number, position, source_sha256, CAST(calculated_current AS VARCHAR) AS calculated_current
  FROM read_json('$DATA/article_calculated_current.jsonl', format='newline_delimited',
    columns={law_id: 'INTEGER', number: 'VARCHAR', position: 'INTEGER', source_sha256: 'VARCHAR', calculated_current: 'JSON'})
) TO '$OUT/article_calculated_current.parquet' (FORMAT parquet, COMPRESSION zstd);" >/dev/null
echo "article_calculated_current: $(du -h "$OUT/article_calculated_current.parquet" | cut -f1)"
