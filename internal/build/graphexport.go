package build

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ExportGraph writes the graph from the database: nodes.csv and edges.csv
// (for spreadsheets, pandas, Gephi, Neo4j's CSV import) and graph.graphml
// (for Gephi, Cytoscape, networkx). onlyLinked keeps the nodes that have at
// least one edge, which is most of what a picture of the graph needs.
func ExportGraph(ctx context.Context, dsn, dir string, onlyLinked bool) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	nodes := `SELECT key, type, id, article, label, title, dated, status FROM graph_node`
	if onlyLinked {
		nodes = `SELECT n.key, n.type, n.id, n.article, n.label, n.title, n.dated, n.status FROM graph_node n
			WHERE EXISTS (SELECT 1 FROM graph_edge e WHERE e.from_key = n.key OR e.to_key = n.key)`
	}
	edges := `SELECT id, from_key, to_key, relation, source, status, to_cite, part, how, quote, model
		FROM graph_edge ORDER BY id`
	for name, q := range map[string]string{"nodes.csv": nodes + " ORDER BY 1", "edges.csv": edges} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if _, err := conn.PgConn().CopyTo(ctx, f, "COPY ("+q+") TO STDOUT WITH (FORMAT csv, HEADER)"); err != nil {
			f.Close()
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	f, err := os.Create(filepath.Join(dir, "graph.graphml"))
	if err != nil {
		return err
	}
	defer f.Close()
	return writeGraphML(ctx, conn, f, nodes, edges)
}

func writeGraphML(ctx context.Context, conn *pgx.Conn, w io.Writer, nodes, edges string) error {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<graphml xmlns="http://graphml.graphdrawing.org/xmlns">
<key id="type" for="node" attr.name="type" attr.type="string"/>
<key id="label" for="node" attr.name="label" attr.type="string"/>
<key id="title" for="node" attr.name="title" attr.type="string"/>
<key id="dated" for="node" attr.name="dated" attr.type="string"/>
<key id="relation" for="edge" attr.name="relation" attr.type="string"/>
<key id="source" for="edge" attr.name="source" attr.type="string"/>
<key id="status" for="edge" attr.name="status" attr.type="string"/>
<graph id="lebanese-legislation" edgedefault="directed">
`)
	rows, err := conn.Query(ctx, nodes)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key, typ string
		var id int
		var article, label, title, status *string
		var dated pgtype.Date
		if err := rows.Scan(&key, &typ, &id, &article, &label, &title, &dated, &status); err != nil {
			rows.Close()
			return err
		}
		fmt.Fprintf(w, `<node id="%s"><data key="type">%s</data>`, esc(key), esc(typ))
		if label != nil {
			fmt.Fprintf(w, `<data key="label">%s</data>`, esc(*label))
		}
		if title != nil {
			fmt.Fprintf(w, `<data key="title">%s</data>`, esc(*title))
		}
		if dated.Valid {
			fmt.Fprintf(w, `<data key="dated">%s</data>`, dated.Time.Format("2006-01-02"))
		}
		fmt.Fprint(w, "</node>\n")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// Edges whose target isn't in our data have no node to point at: GraphML
	// keeps only linked ones; edges.csv keeps them all, with to_cite.
	rows, err = conn.Query(ctx, `SELECT id, from_key, to_key, relation, source, status FROM graph_edge
		WHERE to_key IS NOT NULL ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		var from, to, rel, src, st string
		if err := rows.Scan(&id, &from, &to, &rel, &src, &st); err != nil {
			return err
		}
		fmt.Fprintf(w, `<edge id="e%d" source="%s" target="%s"><data key="relation">%s</data><data key="source">%s</data><data key="status">%s</data></edge>`+"\n",
			id, esc(from), esc(to), esc(rel), esc(src), esc(st))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = fmt.Fprint(w, "</graph>\n</graphml>\n")
	return err
}
