package releasedb

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/taktekhq/lebanese-legislation/internal/release"
)

// CheckReceipt verifies that the database holds exactly one restore receipt
// matching its release row, on PostgreSQL 18.6 with pgvector 0.8.2, and,
// when localManifest is set, that the receipt is that release.
func CheckReceipt(ctx context.Context, dsn, localManifest string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(ctx, `SELECT t.manifest,r.manifest,r.release_id,current_setting('server_version_num')::int,
		(SELECT extversion FROM pg_extension WHERE extname='vector')
		FROM public.restore_receipt t CROSS JOIN public.lebleg_release r WHERE t.singleton`)
	if err != nil {
		return errors.New("one verified release receipt is required")
	}
	type row struct {
		receipt, internal []byte
		id                string
		version           int
		vector            *string
	}
	var got []row
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.receipt, &x.internal, &x.id, &x.version, &x.vector); err != nil {
			rows.Close()
			return err
		}
		got = append(got, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(got) != 1 {
		return errors.New("one verified release receipt is required")
	}
	x := got[0]
	r, err := release.Parse(x.receipt)
	if err != nil {
		return err
	}
	if x.version != 180006 || x.vector == nil || *x.vector != "0.8.2" || x.id != r.ReleaseID {
		return errors.New("release runtime mismatch")
	}
	var internal map[string]any
	if err := json.Unmarshal(x.internal, &internal); err != nil {
		return errors.New("release receipt mismatch")
	}
	for _, k := range []string{"source", "embedding", "counts", "text_search"} {
		if !release.Equal(r.Raw[k], internal[k]) {
			return errors.New("release receipt mismatch")
		}
	}
	if localManifest != "" {
		want, err := release.Load(localManifest)
		if err != nil {
			return err
		}
		if want.Checksum() != r.Checksum() {
			return errors.New("selected local manifest does not match restored volume")
		}
	}
	return nil
}
