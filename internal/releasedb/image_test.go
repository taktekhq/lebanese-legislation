package releasedb

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDBImageFirstBootAndRestart runs the db image (LEBLEG_TEST_DB_IMAGE)
// on a local release: the first start restores it and turns healthy, a
// restart reuses the volume and turns healthy again, and the reader can
// read. A missing local manifest stops the first start.
func TestDBImageFirstBootAndRestart(t *testing.T) {
	image := os.Getenv("LEBLEG_TEST_DB_IMAGE")
	if image == "" {
		t.Skip("set LEBLEG_TEST_DB_IMAGE to an image built with db/Dockerfile --target db")
	}
	out, _ := built(t)
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "lt-db-image-" + strings.ReplaceAll(t.Name(), "/", "-")
	docker := func(args ...string) (string, error) {
		b, err := exec.Command("docker", args...).CombinedOutput()
		return strings.TrimSpace(string(b)), err
	}
	_, _ = docker("rm", "-f", name)
	t.Cleanup(func() { _, _ = docker("rm", "-f", "-v", name) })
	if msg, err := docker("run", "-d", "--name", name, "-e", "POSTGRES_USER=lebleg_owner", "-e", "POSTGRES_DB=lebleg",
		"-e", "POSTGRES_PASSWORD=owner", "-e", "LEBLEG_READER_PASSWORD=reader", "-e", "LEBLEG_MANIFEST_FILE=/release/manifest.json",
		"-v", out+":/release:ro", image); err != nil {
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
	read, err := docker("exec", "-e", "PGPASSWORD=reader", name, "psql", "-XtA", "-h", "127.0.0.1", "-U", "lebleg_reader", "-d", "lebleg",
		"-c", "SELECT count(*) FROM legislation")
	if err != nil || read != "3" {
		t.Fatalf("reader read %q: %v", read, err)
	}
	if _, err := docker("restart", name); err != nil {
		t.Fatal(err)
	}
	waitHealthy()
	logs, _ := docker("logs", name)
	if strings.Count(logs, "restored") != 1 {
		t.Fatalf("the restart restored again or never restored:\n%s", logs)
	}

	missing := name + "-missing"
	t.Cleanup(func() { _, _ = docker("rm", "-f", "-v", missing) })
	if msg, err := docker("run", "-d", "--name", missing, "-e", "POSTGRES_PASSWORD=owner",
		"-e", "LEBLEG_MANIFEST_FILE=/release/none.json", "-v", out+":/release:ro", image); err != nil {
		t.Fatal(msg)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		status, _ := docker("inspect", "-f", "{{.State.Status}}", missing)
		if status == "exited" {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("a missing manifest did not stop the first start")
}
