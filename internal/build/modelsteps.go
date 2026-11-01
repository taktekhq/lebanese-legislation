package build

import (
	"fmt"

	"github.com/taktekhq/lebanese-legislation/db"
	"github.com/taktekhq/lebanese-legislation/internal/modelout"
)

// The build's steps that use model output run in internal/modelout, which
// writes no law text and hands back only counts and decisions: this package,
// whose writers store law text, never reads model output itself (the
// wording guard's static check, internal/provenance ScanWriters).

// titlesEn loads the model's English titles (legislation_title_en).
func (b *builder) titlesEn() error { return modelout.TitlesEn(b.ctx, b.conn, b.laws, b.rep.note) }

// rulingFiles loads the scanned judgments and their machine readings.
func (b *builder) rulingFiles() error {
	if b.RulingFiles == "" {
		return nil
	}
	st, err := modelout.RulingFiles(b.ctx, b.conn, b.RulingFiles, b.rep.note)
	b.rep.RulingFiles, b.rep.RulingsRead, b.rep.RulingPages = st.Files, st.Read, st.Pages
	return err
}

// graph writes graph_edge from the model's citations, the Centre's links,
// the gazette, the pattern pass and db/amounts.tsv.
func (b *builder) graph() error {
	// A graph-only rerun has no prior-state step: read the ids before
	// GraphSchema replaces the table.
	if err := b.readPriorEdges(); err != nil {
		return err
	}
	if _, err := b.conn.Exec(b.ctx, db.GraphSchema); err != nil {
		return err
	}
	ix, err := b.lawIndex()
	if err != nil {
		return err
	}
	numbers, err := b.articleNumbers()
	if err != nil {
		return err
	}
	st, err := modelout.Graph(b.ctx, b.conn, modelout.GraphInput{Index: ix, Numbers: numbers, Citations: b.Citations,
		RulingCitations: b.RulingCitations, Indexes: b.Only == "graph", PriorIDs: b.priorEdges, Note: b.rep.note})
	if err != nil {
		return err
	}
	b.rep.ArtNum = st.ArtNum
	b.rep.Graph = make(map[string]int, len(st.Edges))
	for k, n := range st.Edges {
		b.rep.Graph[string(k)] = n
	}
	return nil
}

// readPriorEdges keeps graph_edge's ids from the database being replaced, once
// per build, so the same edge keeps its id (modelout/edgeids.go).
func (b *builder) readPriorEdges() error {
	if b.priorEdgesRead {
		return nil
	}
	prior, err := modelout.ReadPriorEdgeIDs(b.ctx, b.conn)
	if err != nil {
		return fmt.Errorf("graph_edge ids: %w", err)
	}
	b.priorEdges, b.priorEdgesRead = prior, true
	return nil
}
