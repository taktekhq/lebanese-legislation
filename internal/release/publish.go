package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/taktekhq/lebanese-legislation/internal/hfhub"
)

// PlannedFile is one file a publish adds: Content, or the file at Path.
type PlannedFile struct {
	Content []byte
	Path    string
}

// Plan verifies a built release directory (manifest.json and its dump) and
// returns what a publish adds to the dataset, keyed by path in the dataset:
// releases/<id>/manifest.json (byte for byte), releases/<id>/<dump> and the
// releases/latest.json pointer.
func Plan(dir string) (*Release, map[string]PlannedFile, error) {
	manifestPath := filepath.Join(dir, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	r, err := Parse(raw)
	if err != nil {
		return nil, nil, err
	}
	dump := filepath.Join(dir, r.Dump.Path)
	if err := VerifyFile(dump, *r.Dump); err != nil {
		return nil, nil, err
	}
	prefix := "releases/" + r.ReleaseID
	sum := sha256.Sum256(raw)
	pointer, err := json.MarshalIndent(Pointer{FormatVersion: 1, ReleaseID: r.ReleaseID,
		ManifestPath: prefix + "/manifest.json", ManifestSHA256: hex.EncodeToString(sum[:])}, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return r, map[string]PlannedFile{
		prefix + "/manifest.json":  {Content: raw},
		prefix + "/" + r.Dump.Path: {Path: dump},
		LatestPath:                 {Content: append(pointer, '\n')},
	}, nil
}

// The managed block of the dataset card, between these markers.
const (
	CardStart = "<!-- lebleg-release:start -->"
	CardEnd   = "<!-- lebleg-release:end -->"
)

// UpdateCardBlock replaces the managed block in a dataset card, or inserts
// it after the first "# " heading, leaving the YAML header and everything
// else untouched.
func UpdateCardBlock(text, body string) (string, error) {
	block := CardStart + "\n" + strings.TrimSpace(body) + "\n" + CardEnd
	if strings.Contains(text, CardStart) || strings.Contains(text, CardEnd) {
		if strings.Count(text, CardStart) != 1 || strings.Count(text, CardEnd) != 1 || strings.Index(text, CardEnd) < strings.Index(text, CardStart) {
			return "", errors.New("invalid managed dataset block")
		}
		return text[:strings.Index(text, CardStart)] + block + text[strings.Index(text, CardEnd)+len(CardEnd):], nil
	}
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			out := append([]string{}, lines[:i+1]...)
			if !strings.HasSuffix(line, "\n") {
				out[i] += "\n"
			}
			out = append(out, "\n"+block+"\n")
			out = append(out, lines[i+1:]...)
			return strings.Join(out, ""), nil
		}
	}
	return "", errors.New("dataset card has no heading")
}

// CardBody is the managed block's text for a published release.
func CardBody(repo string, r *Release, setupURL string) string {
	base := "https://huggingface.co/datasets/" + repo
	body := fmt.Sprintf("**PostgreSQL + pgvector:** [Download the database dump](%s/resolve/main/releases/%s/%s?download=true). %s source records and %s indexed passages.",
		base, r.ReleaseID, r.Dump.Path, thousands(r.Counts.SourceDocuments), thousands(r.Counts.SemanticChunks))
	if setupURL != "" {
		body += fmt.Sprintf(" [Run with Docker](%s).", setupURL)
	}
	return body
}

func thousands(n int64) string {
	s := fmt.Sprint(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// PublishOptions are the choices of a publish.
type PublishOptions struct {
	Repo string
	// ExpectLatest, when set, is the release id releases/latest.json must
	// name now, so a publish never replaces a release newer than the one the
	// build started from. "none" means no pointer may exist yet.
	ExpectLatest string
	// Card updates the managed block of README.md in the same commit.
	Card     bool
	SetupURL string
	Message  string
}

// ErrExists refuses to overwrite a release that is already published.
var ErrExists = errors.New("release ID already exists; choose a new release ID")

// Publish uploads a verified release directory to the dataset in one commit:
// the release files, the latest pointer, an LFS rule for *.dump in
// .gitattributes when missing, and the card block. It never overwrites a
// published release. It returns the new dataset commit.
func Publish(ctx context.Context, hub *hfhub.Client, dir string, o PublishOptions) (string, *Release, error) {
	r, plan, err := Plan(dir)
	if err != nil {
		return "", nil, err
	}
	head, err := hub.Revision(ctx, hfhub.Dataset, o.Repo, "main")
	if err != nil {
		return "", nil, err
	}
	files, err := hub.List(ctx, hfhub.Dataset, o.Repo, head, "")
	if err != nil {
		return "", nil, err
	}
	existing := map[string]bool{}
	for _, f := range files {
		existing[f.Path] = true
	}
	for p := range plan {
		if p != LatestPath && existing[p] {
			return "", nil, ErrExists
		}
	}
	if o.ExpectLatest != "" {
		current := "none"
		if existing[LatestPath] {
			b, err := hub.Read(ctx, hfhub.Dataset, o.Repo, head, LatestPath, maxMetadata)
			if err != nil {
				return "", nil, err
			}
			var p Pointer
			if err := json.Unmarshal(b, &p); err != nil {
				return "", nil, errors.New("invalid latest pointer")
			}
			current = p.ReleaseID
		}
		if current != o.ExpectLatest {
			return "", nil, fmt.Errorf("latest release is %s, not %s; reconcile before publishing", current, o.ExpectLatest)
		}
	}
	var attributes []byte
	if existing[".gitattributes"] {
		if attributes, err = hub.Read(ctx, hfhub.Dataset, o.Repo, head, ".gitattributes", maxMetadata); err != nil {
			return "", nil, err
		}
	}
	commit := hfhub.Commit{Branch: "main", Parent: head, Message: o.Message}
	if commit.Message == "" {
		commit.Message = "Publish verified PostgreSQL and pgvector release"
	}
	if !bytes.Contains(attributes, []byte("*.dump filter=lfs")) {
		commit.Files = append(commit.Files, hfhub.File{PathInRepo: ".gitattributes",
			Content: append(bytes.TrimRight(attributes, " \t\r\n"), []byte("\n*.dump filter=lfs diff=lfs merge=lfs -text\n")...)})
	}
	for _, p := range sortedKeys(plan) {
		f := plan[p]
		commit.Files = append(commit.Files, hfhub.File{PathInRepo: p, Content: f.Content, Path: f.Path, LFS: f.Path != ""})
	}
	if o.Card {
		if !existing["README.md"] {
			return "", nil, errors.New("dataset has no README.md for the release block; publish without -card")
		}
		card, err := hub.Read(ctx, hfhub.Dataset, o.Repo, head, "README.md", 4<<20)
		if err != nil {
			return "", nil, err
		}
		updated, err := UpdateCardBlock(string(card), CardBody(o.Repo, r, o.SetupURL))
		if err != nil {
			return "", nil, err
		}
		if updated != string(card) {
			commit.Files = append(commit.Files, hfhub.File{PathInRepo: "README.md", Content: []byte(updated)})
		}
	}
	oid, err := hub.CreateCommit(ctx, hfhub.Dataset, o.Repo, commit)
	return oid, r, err
}

func sortedKeys(m map[string]PlannedFile) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// VerifyPublished checks, anonymously, that a published release reads back
// as built: the manifest is identical, the Hub stores the dump with the
// manifest's size and SHA-256 in LFS, and its first and last 64 KiB match
// localDump (when given).
func VerifyPublished(ctx context.Context, anon *hfhub.Client, repo, revision string, r *Release, localDump string) error {
	prefix := "releases/" + r.ReleaseID
	raw, err := anon.Read(ctx, hfhub.Dataset, repo, revision, prefix+"/manifest.json", maxMetadata)
	if err != nil {
		return err
	}
	got, err := Parse(raw)
	if err != nil || !Equal(got.External(), r.External()) {
		return errors.New("published manifest differs")
	}
	dumpPath := prefix + "/" + r.Dump.Path
	info, err := anon.PathsInfo(ctx, hfhub.Dataset, repo, revision, []string{dumpPath})
	if err != nil {
		return err
	}
	if len(info) != 1 || info[0].Size != r.Dump.SizeBytes || info[0].LFS == nil || info[0].LFS.SHA256 != r.Dump.SHA256 {
		return errors.New("published dump identity differs")
	}
	size := r.Dump.SizeBytes
	for _, span := range [][2]int64{{0, min(size, 65536) - 1}, {max(0, size-65536), size - 1}} {
		res, err := anon.Open(ctx, hfhub.Dataset, repo, revision, dumpPath, fmt.Sprintf("bytes=%d-%d", span[0], span[1]))
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, span[1]-span[0]+2))
		status := res.StatusCode
		res.Body.Close()
		if err != nil {
			return err
		}
		if status != http.StatusPartialContent && !(status == http.StatusOK && span[0] == 0 && span[1] == size-1) {
			return errors.New("published dump range download failed")
		}
		if int64(len(data)) != span[1]-span[0]+1 {
			return errors.New("published dump range length differs")
		}
		if localDump != "" {
			f, err := os.Open(localDump)
			if err != nil {
				return err
			}
			want := make([]byte, len(data))
			_, err = f.ReadAt(want, span[0])
			f.Close()
			if err != nil || !bytes.Equal(want, data) {
				return errors.New("published dump differs from the local dump")
			}
		}
	}
	return nil
}
