package releasedb

import (
	"bufio"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

//go:embed sql/*.sql
var sqlFiles embed.FS

func resource(name string) string {
	b, err := sqlFiles.ReadFile("sql/" + name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// GoWhitespace is the Unicode White_Space set Go's unicode.IsSpace uses: the
// characters a text made only of is blank, and that split an article slug.
const GoWhitespace = "\t\n\v\f\r \u0085                 　"

func digest(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// publicTables are the tables of the public schema, in file order.
func publicTables() []string {
	re := regexp.MustCompile(`(?m)^CREATE TABLE ([a-z_]+)`)
	var out []string
	for _, m := range re.FindAllStringSubmatch(resource("public_schema.sql"), -1) {
		out = append(out, m[1])
	}
	return out
}

// quote percent-encodes everything but ASCII letters, digits and "-._~",
// as Python's urllib.parse.quote(s, safe=”) does.
func quote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-._~", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// sourceDetails is a document's public URL and citation metadata.
func sourceDetails(kind string, sourceID int64, part, lang string, fields map[string]any) (string, map[string]any) {
	sourceType := kind
	if v, ok := fields["kind"].(string); ok && v != "" {
		sourceType = v
	}
	route := kind
	if kind == "law" || kind == "article" {
		switch {
		case strings.HasPrefix(sourceType, "مرسوم"):
			route = "decree"
		case strings.HasPrefix(sourceType, "قانون") || sourceType == "الدستور" || sourceType == "دستور":
			route = "law"
		default:
			route = "text"
		}
	}
	if kind == "doctrine" {
		route = "doctrine"
	}
	url := fmt.Sprintf("https://lebaneselegislation.com/%s/%d/", route, sourceID)
	if kind == "article" {
		spaced := strings.Map(func(r rune) rune {
			if strings.ContainsRune(GoWhitespace, r) {
				return ' '
			}
			return r
		}, part)
		var words []string
		for _, w := range strings.Split(spaced, " ") {
			if w != "" {
				words = append(words, w)
			}
		}
		url += "article/" + quote(strings.Join(words, "-")) + "/"
	}
	metadata := map[string]any{"source_type": sourceType, "language": lang, "english_unofficial": lang == "en",
		"authoritative_language": "ar"}
	dates := map[string]bool{"issued_on": true, "decided_on": true, "signed_on": true, "published_on": true, "date": true, "read_at": true}
	for _, key := range []string{"number", "issued_on", "decided_on", "signed_on", "published_on", "date", "year", "low_confidence", "source_url", "source", "confidence", "model", "read_at"} {
		v, ok := fields[key]
		if !ok || v == nil {
			continue
		}
		name := key
		if key == "number" {
			name = "identifier"
		}
		if dates[key] {
			v = fmt.Sprint(v)
		}
		metadata[name] = v
	}
	if _, ok := metadata["date"]; !ok {
		for _, key := range []string{"issued_on", "decided_on", "signed_on", "published_on"} {
			if v, ok := metadata[key]; ok {
				metadata["date"] = v
				break
			}
		}
	}
	return url, metadata
}

type column struct{ name, udt string }

func columns(ctx context.Context, q querier, table string) ([]column, error) {
	rows, err := q.Query(ctx, `SELECT column_name,udt_name FROM information_schema.columns
		WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (column, error) {
		var c column
		err := r.Scan(&c.name, &c.udt)
		return c, err
	})
}

// copyTable streams one table's declared columns from the source database
// into the target with COPY, in PostgreSQL's text format.
func copyTable(ctx context.Context, tx pgx.Tx, source *pgx.Conn, table string, declared []column) (int64, error) {
	have, err := columns(ctx, source, table)
	if err != nil {
		return 0, err
	}
	if len(have) == 0 {
		return 0, fmt.Errorf("%s: missing from the source database", table)
	}
	types := map[string]string{}
	for _, c := range have {
		types[c.name] = c.udt
	}
	var selects, names []string
	join := ""
	for _, c := range declared {
		ident := pgx.Identifier{c.name}.Sanitize()
		names = append(names, ident)
		udt, ok := types[c.name]
		if !ok && table == "legislation" && c.name == "title_en" {
			// The public legislation table carries its unofficial English title.
			selects = append(selects, "te.title_en")
			join = " LEFT JOIN public.legislation_title_en te ON te.law_id=s.id"
			continue
		}
		if !ok {
			return 0, fmt.Errorf("%s: column contract differs (no %s in the source)", table, c.name)
		}
		if udt != c.udt {
			return 0, fmt.Errorf("%s.%s: source type %s differs from public type %s", table, c.name, udt, c.udt)
		}
		selects = append(selects, "s."+ident)
	}
	query := fmt.Sprintf("COPY (SELECT %s FROM public.%s s%s) TO STDOUT", strings.Join(selects, ","), pgx.Identifier{table}.Sanitize(), join)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := source.PgConn().CopyTo(ctx, pw, query)
		pw.CloseWithError(err)
		done <- err
	}()
	tag, err := tx.Conn().PgConn().CopyFrom(ctx, pr, fmt.Sprintf("COPY %s (%s) FROM STDIN", pgx.Identifier{table}.Sanitize(), strings.Join(names, ",")))
	pr.CloseWithError(err)
	if srcErr := <-done; srcErr != nil && err == nil {
		err = srcErr
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %w", table, err)
	}
	return tag.RowsAffected(), nil
}

// calculatedRow is one line of data/article_calculated_current.jsonl.
type calculatedRow struct {
	LawID             *int64          `json:"law_id"`
	Number            *string         `json:"number"`
	Position          *int64          `json:"position"`
	SourceSHA256      *string         `json:"source_sha256"`
	CalculatedCurrent json.RawMessage `json:"calculated_current"`
}

func loadCalculated(ctx context.Context, tx pgx.Tx, path string) (int64, error) {
	if path == "" {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var n int64
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row calculatedRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return 0, fmt.Errorf("invalid calculated_current JSON: %w", err)
		}
		if row.LawID == nil || row.Number == nil || row.Position == nil || row.SourceSHA256 == nil || !json.Valid(row.CalculatedCurrent) {
			return 0, errors.New("invalid calculated_current JSON")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO article_calculated_current(law_id,number,position,source_sha256,calculated_current)
			VALUES($1,$2,$3,$4,$5::jsonb)`, *row.LawID, *row.Number, *row.Position, *row.SourceSHA256, string(row.CalculatedCurrent)); err != nil {
			return 0, err
		}
		n++
	}
	return n, sc.Err()
}

// importSource creates the public tables, copies them from the source
// database (a database loaded from data/ by data/load.sh), loads the
// calculated current amounts from their JSON lines file, checks them, then
// adds the public constraints, foreign keys last.
func importSource(ctx context.Context, tx pgx.Tx, source *pgx.Conn, calculated string, asOf time.Time) (map[string]int64, error) {
	schema := resource("public_schema.sql")
	i := strings.Index(schema, "ALTER TABLE")
	ddl, constraints := schema[:i], schema[i:]
	if _, err := tx.Exec(ctx, ddl); err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, table := range publicTables() {
		if table == "article_calculated_current" {
			n, err := loadCalculated(ctx, tx, calculated)
			if err != nil {
				return nil, err
			}
			counts[table] = n
			continue
		}
		declared, err := columns(ctx, tx, table)
		if err != nil {
			return nil, err
		}
		n, err := copyTable(ctx, tx, source, table, declared)
		if err != nil {
			return nil, err
		}
		counts[table] = n
	}
	if err := validateCalculatedRows(ctx, tx, asOf); err != nil {
		return nil, err
	}
	statements := strings.Split(constraints, ";")
	for _, foreign := range []bool{false, true} {
		for _, st := range statements {
			if strings.TrimSpace(st) != "" && strings.Contains(st, "FOREIGN KEY") == foreign {
				if _, err := tx.Exec(ctx, st); err != nil {
					return nil, err
				}
			}
		}
	}
	return counts, nil
}

// ErrCalculated is a calculated current amount the release refuses.
var ErrCalculated = errors.New("invalid calculated_current")

// validateCalculated checks one exported calculation against its source
// text: the scope (one law), the authority (decree 13909/2024 article 1, in
// force from 19 September 2024), the multiplier and the exact source hash.
// The arithmetic itself belongs to the Go exporter.
func validateCalculated(lawID int64, number string, hashed string, obj map[string]any, sourceText *string, asOf time.Time) error {
	bad := fmt.Errorf("%w source, scope or authority", ErrCalculated)
	if lawID != 244565 || number == "" || obj == nil {
		return bad
	}
	if obj["kind"] != "statutory_monetary_adjustment" || obj["is_published_wording"] != false {
		return bad
	}
	if m, ok := obj["multiplier"].(json.Number); !ok || m.String() != "50" {
		return bad
	}
	authority, _ := obj["authority"].(map[string]any)
	wantAuthority := map[string]any{"law_id": json.Number("330819"), "article": "1",
		"url": "https://lebaneselegislation.com/decree/330819/article/1/", "effective_on": "2024-09-19"}
	if len(authority) != len(wantAuthority) {
		return bad
	}
	for k, v := range wantAuthority {
		if authority[k] != v {
			return bad
		}
	}
	if asOf.Before(time.Date(2024, 9, 19, 0, 0, 0, 0, time.UTC)) {
		return bad
	}
	if sourceText == nil || *sourceText == "" || obj["source_text_ar"] != *sourceText || hashed != digest(*sourceText) || obj["source_sha256"] != hashed {
		return bad
	}
	text, ok := obj["text_ar"].(string)
	if !ok || strings.TrimSpace(text) == "" || text == *sourceText {
		return bad
	}
	return nil
}

func validateCalculatedRows(ctx context.Context, q querier, asOf time.Time) error {
	rows, err := q.Query(ctx, `SELECT c.law_id,c.number,c.position,c.source_sha256,c.calculated_current,
		f.text_ar,f.state,f.pending,f.valid_from,l.status,l.issued_on
		FROM article_calculated_current c
		LEFT JOIN article_in_force f ON (f.law_id,f.number,f.position)=(c.law_id,c.number,c.position)
		LEFT JOIN legislation l ON l.id=c.law_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var (
			lawID, position     int64
			number, hashed      string
			raw                 []byte
			text, state, status *string
			pending             *bool
			validFrom, issuedOn *time.Time
		)
		if err := rows.Scan(&lawID, &number, &position, &hashed, &raw, &text, &state, &pending, &validFrom, &status, &issuedOn); err != nil {
			return err
		}
		key := fmt.Sprintf("%d\x00%s", lawID, number)
		if seen[key] || state == nil || *state != "in_force" || pending == nil || *pending ||
			status == nil || *status == "repealed" || *status == "annulled" ||
			validFrom != nil && validFrom.After(asOf) || issuedOn != nil && issuedOn.After(asOf) {
			return fmt.Errorf("%w article eligibility or duplicate", ErrCalculated)
		}
		seen[key] = true
		var obj map[string]any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		if err := d.Decode(&obj); err != nil {
			return fmt.Errorf("%w JSON", ErrCalculated)
		}
		if err := validateCalculated(lawID, number, hashed, obj, text, asOf); err != nil {
			return err
		}
	}
	return rows.Err()
}

// deriveDocuments creates source_documents, the canonical texts the search
// and the embeddings use, from the imported tables as of a date.
func deriveDocuments(ctx context.Context, tx pgx.Tx, asOf time.Time) (int64, error) {
	date := asOf.Format("2006-01-02")
	if _, err := tx.Exec(ctx, "SELECT set_config('lebleg.as_of_date',$1,true)", date); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE source_documents (
	  kind text NOT NULL,source_id integer NOT NULL,part text NOT NULL,lang text NOT NULL,
	  title text NOT NULL,text text NOT NULL,hash text NOT NULL,source_url text NOT NULL,metadata jsonb NOT NULL,
	  PRIMARY KEY(kind,source_id,part,lang))`); err != nil {
		return 0, err
	}
	corpus := strings.ReplaceAll(resource("corpus.sql"), "CURRENT_DATE", "current_setting('lebleg.as_of_date')::date")
	query := `SELECT c.kind,c.source_id,c.part,c.lang,c.title,c.body,CASE WHEN l.id IS NOT NULL THEN jsonb_build_object('kind',l.kind,'number',l.number,'issued_on',l.issued_on)
	  WHEN r.id IS NOT NULL THEN jsonb_build_object('number',r.number,'year',r.year,'decided_on',r.decided_on,
	    'model',rp.model,'read_at',rp.read_at,'low_confidence',rp.low_confidence)
	  WHEN o.id IS NOT NULL THEN jsonb_build_object('number',o.number,'year',o.year,'issued_on',o.issued_on)
	  WHEN a.id IS NOT NULL THEN jsonb_build_object('signed_on',a.signed_on)
	  WHEN rs.id IS NOT NULL THEN jsonb_build_object('published_on',rs.published_on)
	  ELSE '{}'::jsonb END FROM (` + corpus + `) c
	  LEFT JOIN legislation l ON c.kind IN ('law','article') AND l.id=c.source_id
	  LEFT JOIN ruling r ON c.kind='ruling' AND r.id=c.source_id
	  LEFT JOIN ruling_page rp ON c.kind='ruling' AND rp.ruling_id=c.source_id AND rp.n::text=c.part
	  LEFT JOIN opinion o ON c.kind='opinion' AND o.id=c.source_id
	  LEFT JOIN agreement a ON c.kind='agreement' AND a.id=c.source_id
	  LEFT JOIN research rs ON c.kind='research' AND rs.id=c.source_id
	  WHERE btrim(coalesce(c.body,''))<>''`
	if _, err := tx.Exec(ctx, "DECLARE canonical_documents NO SCROLL CURSOR FOR "+query); err != nil {
		return 0, err
	}
	var total int64
	for {
		rows, err := tx.Query(ctx, "FETCH 2000 FROM canonical_documents")
		if err != nil {
			return 0, err
		}
		var batch [][]any
		for rows.Next() {
			var (
				kind, part, lang string
				id               int64
				title            *string
				text             string
				raw              []byte
			)
			if err := rows.Scan(&kind, &id, &part, &lang, &title, &text, &raw); err != nil {
				rows.Close()
				return 0, err
			}
			var fields map[string]any
			d := json.NewDecoder(strings.NewReader(string(raw)))
			d.UseNumber()
			if err := d.Decode(&fields); err != nil {
				rows.Close()
				return 0, err
			}
			url, metadata := sourceDetails(kind, id, part, lang, fields)
			meta, err := json.Marshal(metadata)
			if err != nil {
				rows.Close()
				return 0, err
			}
			t := ""
			if title != nil {
				t = *title
			}
			batch = append(batch, []any{kind, id, part, lang, t, text, digest(text), url, string(meta)})
		}
		if err := rows.Err(); err != nil {
			return 0, err
		}
		if len(batch) == 0 {
			break
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"source_documents"},
			[]string{"kind", "source_id", "part", "lang", "title", "text", "hash", "source_url", "metadata"},
			pgx.CopyFromRows(batch)); err != nil {
			return 0, err
		}
		total += int64(len(batch))
	}
	if _, err := tx.Exec(ctx, "CLOSE canonical_documents"); err != nil {
		return 0, err
	}
	if err := refreshMetadata(ctx, tx); err != nil {
		return 0, err
	}
	if err := refreshCalculatedMetadata(ctx, tx, asOf); err != nil {
		return 0, err
	}
	var n int64
	err := tx.QueryRow(ctx, "SELECT count(*) FROM source_documents").Scan(&n)
	return n, err
}

// refreshMetadata refreshes public metadata only, keeping the exact source
// text and hashes.
func refreshMetadata(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, resource("public_metadata.sql")); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT d.source_id,d.part,d.lang,l.kind FROM source_documents d JOIN legislation l ON l.id=d.source_id
		WHERE d.kind='article' AND d.part~$1`, "["+GoWhitespace+"]")
	if err != nil {
		return err
	}
	type special struct {
		id               int64
		part, lang, kind string
	}
	var list []special
	for rows.Next() {
		var s special
		var kind *string
		if err := rows.Scan(&s.id, &s.part, &s.lang, &kind); err != nil {
			rows.Close()
			return err
		}
		if kind != nil {
			s.kind = *kind
		}
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range list {
		url, _ := sourceDetails("article", s.id, s.part, s.lang, map[string]any{"kind": s.kind})
		if _, err := tx.Exec(ctx, `UPDATE source_documents SET source_url=$1 WHERE kind='article' AND source_id=$2 AND part=$3 AND lang=$4
			AND source_url IS DISTINCT FROM $1`, url, s.id, s.part, s.lang); err != nil {
			return err
		}
	}
	return nil
}

func refreshCalculatedMetadata(ctx context.Context, tx pgx.Tx, asOf time.Time) error {
	var present bool
	if err := tx.QueryRow(ctx, "SELECT to_regclass('public.article_calculated_current') IS NOT NULL").Scan(&present); err != nil || !present {
		return err
	}
	if err := validateCalculatedRows(ctx, tx, asOf); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE source_documents d SET metadata=d.metadata || jsonb_build_object('calculated_current',c.calculated_current)
		FROM article_calculated_current c WHERE d.kind='article' AND d.lang='ar'
		AND d.source_id=c.law_id AND d.part=c.number AND d.hash=c.source_sha256
		AND d.text=c.calculated_current->>'source_text_ar'
		AND d.metadata->'calculated_current' IS DISTINCT FROM c.calculated_current`)
	return err
}
