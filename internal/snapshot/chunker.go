package snapshot

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

// chunker writes SQL statements to a sequence of small files
// (data_0001.sql, data_0002.sql, ...), rolling to a new file once the
// current one passes targetBytes. Small files mean each one fits
// comfortably under D1's import limits and keeps memory flat: nothing
// this package writes is ever held as one big in-memory buffer.
type chunker struct {
	dir     string
	target  int64
	idx     int
	w       *bufio.Writer
	f       *os.File
	written int64
	files   []string
}

func newChunker(dir string, targetBytes int64) (*chunker, error) {
	c := &chunker{dir: dir, target: targetBytes}
	if err := c.roll(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *chunker) roll() error {
	if c.f != nil {
		if err := c.w.Flush(); err != nil {
			return err
		}
		if err := c.f.Close(); err != nil {
			return err
		}
	}
	c.idx++
	name := fmt.Sprintf("data_%04d.sql", c.idx)
	f, err := os.Create(filepath.Join(c.dir, name))
	if err != nil {
		return err
	}
	c.f = f
	c.w = bufio.NewWriterSize(f, 256*1024)
	c.written = 0
	c.files = append(c.files, name)
	return nil
}

// writeStmt appends one complete SQL statement (already ending in ";\n"),
// rolling to a new file first if the current one is already past target
// and non-empty, so a statement is never split across two files.
func (c *chunker) writeStmt(stmt string) error {
	if c.written > 0 && c.written+int64(len(stmt)) > c.target {
		if err := c.roll(); err != nil {
			return err
		}
	}
	n, err := c.w.WriteString(stmt)
	c.written += int64(n)
	return err
}

func (c *chunker) close() error {
	if c.f == nil {
		return nil
	}
	if err := c.w.Flush(); err != nil {
		return err
	}
	err := c.f.Close()
	c.f = nil
	return err
}

// batcher groups rows into INSERT statements up to maxStmtBytes, so no
// single statement risks D1's per-statement size limit, then hands each
// finished statement to the chunker.
type batcher struct {
	ch     *chunker
	table  string
	cols   string
	rows   []string
	nbytes int
}

func newBatcher(ch *chunker, table, cols string) *batcher {
	return &batcher{ch: ch, table: table, cols: cols}
}

func (b *batcher) add(tuple string) error {
	if len(b.rows) > 0 && b.nbytes+len(tuple)+1 > maxStmtBytes {
		if err := b.flush(); err != nil {
			return err
		}
	}
	b.rows = append(b.rows, tuple)
	b.nbytes += len(tuple) + 1
	return nil
}

func (b *batcher) flush() error {
	if len(b.rows) == 0 {
		return nil
	}
	stmt := "INSERT INTO " + b.table + " (" + b.cols + ") VALUES "
	for i, r := range b.rows {
		if i > 0 {
			stmt += ","
		}
		stmt += r
	}
	stmt += ";\n"
	b.rows = b.rows[:0]
	b.nbytes = 0
	return b.ch.writeStmt(stmt)
}
