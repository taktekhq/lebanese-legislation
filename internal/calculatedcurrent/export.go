package calculatedcurrent

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
)

// Gate is the wording guard (provenance.Guard). An interface, so the web
// server, which only calculates, does not link the guard and the build.
type Gate interface {
	Gate(ctx context.Context, dsn string) error
}

// Export verifies provenance before atomically writing a derived JSONL sidecar.
// It never writes to the source database.
func Export(ctx context.Context, dsn, output string, guard Gate) (int, error) {
	if err := guard.Gate(ctx, dsn); err != nil {
		return 0, err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close(context.Background()) }()
	rows, err := conn.Query(ctx, exportQuery)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	return writeDestination(rows, output, os.Stdout)
}

func writeDestination(rows pgx.Rows, output string, stdout io.Writer) (int, error) {
	if output == "-" {
		return writeRows(rows, stdout)
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".calculated-current-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	n, writeErr := writeRows(rows, file)
	closeErr := file.Close()
	if writeErr != nil {
		return n, writeErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	return n, os.Rename(file.Name(), output)
}

type exportRow struct {
	LawID             int     `json:"law_id"`
	Number            string  `json:"number"`
	Position          int     `json:"position"`
	SourceSHA256      string  `json:"source_sha256"`
	CalculatedCurrent *Record `json:"calculated_current"`
}

func writeRows(rows pgx.Rows, w io.Writer) (int, error) {
	encoder := json.NewEncoder(w)
	n := 0
	for rows.Next() {
		var in Input
		var position int
		var validFrom *time.Time
		if err := rows.Scan(&in.LawID, &in.Number, &position, &in.Source, &in.State, &in.Pending, &validFrom, &in.Annulled); err != nil {
			return n, err
		}
		in.ValidFrom = validFrom
		record := Calculate(in)
		if record == nil {
			continue
		}
		if err := encoder.Encode(exportRow{in.LawID, in.Number, position, record.SourceSHA256, record}); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// Whole-law repeal follows site Law.sortChanges, including unknown/future dates.
// Repeated numbers use the first position, as public source documents do.
const exportQuery = `SELECT DISTINCT ON (f.law_id, f.number) f.law_id, f.number, coalesce(f.position,0), coalesce(f.text_ar,''), f.state, f.pending,
 greatest(f.valid_from,a.in_force_from),
 (l.status <> 'in_force'
 OR EXISTS (SELECT 1 FROM amendment x WHERE x.law_id=f.law_id
   AND x.article_number IS NULL AND x.action='repeal' AND x.part IS NULL AND x.status='applied')
 OR EXISTS (SELECT 1 FROM council_ruling c WHERE c.law_id=f.law_id AND (c.number='' OR c.number=f.number) AND c.effect IN ('annulled','not_in_force')))
 FROM article_in_force f JOIN legislation l ON l.id=f.law_id
 LEFT JOIN legislation_article a ON a.law_id=f.law_id AND a.number=f.number AND a.position=f.position
 WHERE f.law_id=244565 ORDER BY f.law_id, f.number, f.position NULLS LAST`
