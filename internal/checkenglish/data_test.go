package checkenglish

// LEBLEG_DOCKER_TEST=1 go test ./internal/checkenglish runs the database rules
// against a throwaway postgres:16-alpine (docker), one container, removed after.

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const fixture = `
CREATE TABLE legislation (id integer PRIMARY KEY);
CREATE TABLE source_page (doc_type text, centre_id integer, first_seen timestamptz);
CREATE TABLE legislation_title_en (law_id integer PRIMARY KEY, title_en text NOT NULL, model text NOT NULL);
CREATE TABLE legislation_article (law_id integer, position integer, number text, text_ar text);
`

func conn(t *testing.T) *pgx.Conn {
	t.Helper()
	if os.Getenv("LEBLEG_DOCKER_TEST") != "1" {
		t.Skip("LEBLEG_DOCKER_TEST=1 runs the database rules (docker, postgres:16-alpine)")
	}
	name := "checkenglish-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-e", "POSTGRES_HOST_AUTH_METHOD=trust", "-p", "127.0.0.1::5432", "postgres:16-alpine").CombinedOutput()
	if err != nil {
		t.Fatalf("docker: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	pb, _ := exec.Command("docker", "port", name, "5432/tcp").Output()
	addr := strings.TrimSpace(strings.Split(string(pb), "\n")[0])
	dsn := "postgres://postgres@" + addr + "/postgres?sslmode=disable"
	var c *pgx.Conn
	for i := 0; i < 60; i++ {
		if c, err = pgx.Connect(context.Background(), dsn); err == nil {
			if _, err = c.Exec(context.Background(), "SELECT 1"); err == nil {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	if _, err := c.Exec(context.Background(), fixture); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestData(t *testing.T) {
	ctx := context.Background()
	c := conn(t)
	// Clean: an old text, a new one with a title, an Arabic article.
	c.Exec(ctx, `INSERT INTO legislation VALUES (1), (2)`)
	c.Exec(ctx, `INSERT INTO source_page VALUES ('law', 1, now() - interval '30 days'), ('law', 2, now())`)
	c.Exec(ctx, `INSERT INTO legislation_title_en VALUES (2, 'A title', 'sonnet')`)
	c.Exec(ctx, `INSERT INTO legislation_article VALUES (1, 1, '1', 'المادة الأولى: يعمل بهذا القانون فور نشره')`)
	if v, err := Data(ctx, c, nil); err != nil || len(v) != 0 {
		t.Fatalf("clean: %v %v", v, err)
	}
	// Each fails alone.
	c.Exec(ctx, `INSERT INTO legislation VALUES (3)`)
	c.Exec(ctx, `INSERT INTO source_page VALUES ('law', 3, now())`)
	if v, _ := Data(ctx, c, nil); len(v) != 1 || !strings.Contains(v[0], "text 3") {
		t.Fatalf("new law with no title_en: %v", v)
	}
	c.Exec(ctx, `INSERT INTO legislation_title_en VALUES (3, 'Another', 'sonnet')`)
	c.Exec(ctx, `INSERT INTO legislation_article VALUES (1, 2, '2', 'This article is written in English and is not allowed here')`)
	v, _ := Data(ctx, c, nil)
	if len(v) != 1 || !strings.Contains(v[0], "article 1/2") {
		t.Fatalf("english article: %v", v)
	}
	if v, _ := Data(ctx, c, map[string]bool{"1/2": true}); len(v) != 0 {
		t.Fatalf("allow list: %v", v)
	}
	c.Exec(ctx, `UPDATE legislation_title_en SET model = ' ' WHERE law_id = 3`)
	if v, _ := Data(ctx, c, map[string]bool{"1/2": true}); len(v) != 1 || !strings.Contains(v[0], "no model") {
		t.Fatalf("no model: %v", v)
	}
}
