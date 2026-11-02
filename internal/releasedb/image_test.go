package releasedb

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDBImageFirstBootAndRestart runs the db image (LEBLEG_TEST_DB_IMAGE)
// without a mounted release: the baked database turns healthy, the reader
// can read its laws, and a restart preserves the count and turns healthy again.
func TestDBImageFirstBootAndRestart(t *testing.T) {
	image := os.Getenv("LEBLEG_TEST_DB_IMAGE")
	if image == "" {
		t.Skip("set LEBLEG_TEST_DB_IMAGE to an image built with db/Dockerfile --target db")
	}
	name := "lt-db-image-" + strings.ReplaceAll(t.Name(), "/", "-")
	docker := func(args ...string) (string, error) {
		b, err := exec.Command("docker", args...).CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	_, _ = docker("rm", "-f", name)
	t.Cleanup(func() { _, _ = docker("rm", "-f", "-v", name) })
	if msg, err := docker("run", "-d", "--name", name, image); err != nil {
		t.Fatal(msg)
	}
	waitHealthy := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(deadline) {
			status, _ := docker("inspect", "-f", "{{.State.Status}} {{.State.Health.Status}}", name)
			if status == "running healthy" {
				return
			}
			if strings.HasPrefix(status, "exited") {
				logs, _ := docker("logs", name)
				t.Fatalf("container exited: %s", logs)
			}
			time.Sleep(2 * time.Second)
		}
		logs, _ := docker("logs", name)
		t.Fatalf("not healthy in time: %s", logs)
	}
	waitHealthy()
	readCount := func() int64 {
		t.Helper()
		// bake.sh creates lebleg_web with SELECT grants and loopback trust.
		read, err := docker("exec", name, "psql", "-XtA", "-h", "127.0.0.1", "-U", "lebleg_web", "-d", "lebleg",
			"-c", "SELECT count(*) FROM legislation")
		if err != nil {
			t.Fatalf("reader query: %s: %v", read, err)
		}
		count, err := strconv.ParseInt(read, 10, 64)
		if err != nil || count <= 0 {
			t.Fatalf("reader count %q: want a positive integer: %v", read, err)
		}
		return count
	}
	count := readCount()
	if _, err := docker("restart", name); err != nil {
		t.Fatal(err)
	}
	waitHealthy()
	if restarted := readCount(); restarted != count {
		t.Fatalf("count after restart = %d, want %d", restarted, count)
	}
}
