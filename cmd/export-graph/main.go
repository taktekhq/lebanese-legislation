// Command export-graph writes the citation graph out of the database: nodes.csv, edges.csv, graph.graphml.
//
//	export-graph [-db dsn] [-out dir] [-all-nodes]
package main

import (
	"flag"
	"log"

	"github.com/taktekhq/lebanese-legislation/internal/build"
	"github.com/taktekhq/lebanese-legislation/internal/cli"
)

func main() {
	ctx, stop := cli.Context()
	defer stop()
	// Write the graph out of the database: nodes.csv, edges.csv, graph.graphml.
	dsn := flag.String("db", cli.DefaultDSN(), "Postgres connection string")
	out := flag.String("out", "graph", "directory to write to")
	all := flag.Bool("all-nodes", false, "every item, not only those with a link")
	flag.Parse()
	if err := build.ExportGraph(ctx, *dsn, *out, !*all); err != nil {
		log.Fatal(err)
	}
}
