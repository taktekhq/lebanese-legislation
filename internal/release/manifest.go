// Package release is the public database release: its manifest contract,
// verified downloads from the Hugging Face dataset, and the files a publish
// adds there (releases/<id>/corpus.dump, releases/<id>/manifest.json and the
// releases/latest.json pointer).
package release

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
)

//go:embed VERSION
var version string

// Version is the software version recorded as a manifest's build_version and
// used for the container image tags.
func Version() string { return strings.TrimSpace(version) }

// The embedding every release uses. A release with other settings is refused.
const (
	Model        = "gemini-embedding-001"
	Dimensions   = 768
	TaskType     = "RETRIEVAL_DOCUMENT"
	ChunkSize    = 900
	ChunkOverlap = 120
	Storage      = "halfvec(768)"
)

// SearchIndexes are the text search indexes a release declares.
var SearchIndexes = []string{
	"source_documents_title_trgm",
	"source_documents_text_trgm",
	"source_documents_identifier",
	"source_documents_issued_on",
}

// ErrManifest is wrapped by every manifest validation failure.
var ErrManifest = errors.New("unsupported or invalid release manifest")

var (
	releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	repoPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	revisionPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	tablePattern     = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
)

// ValidReleaseID reports whether id is a safe release id.
func ValidReleaseID(id string) bool { return releaseIDPattern.MatchString(id) }

// ValidRevision reports whether rev is a 40-hex commit.
func ValidRevision(rev string) bool { return revisionPattern.MatchString(rev) }

// ValidSHA256 reports whether s is a lowercase hex SHA-256.
func ValidSHA256(s string) bool { return sha256Pattern.MatchString(s) }

// Source is the input a release was built from.
type Source struct {
	RepoID   string `json:"repo_id"`
	Revision string `json:"revision"`
}

// Postgres is the server a release needs.
type Postgres struct {
	Version       string `json:"version"`
	VectorVersion string `json:"vector_version"`
}

// TextSearch is the text search extension and indexes in a release.
type TextSearch struct {
	Extension string   `json:"extension"`
	Version   string   `json:"version"`
	Indexes   []string `json:"indexes"`
}

// Embedding is how the saved vectors were made.
type Embedding struct {
	Model        string `json:"model"`
	Dimensions   int    `json:"dimensions"`
	TaskType     string `json:"task_type"`
	ChunkSize    int    `json:"chunk_size"`
	ChunkOverlap int    `json:"chunk_overlap"`
	Storage      string `json:"storage,omitempty"`
	Provider     string `json:"provider,omitempty"`
}

// Counts are the rows a restore must find.
type Counts struct {
	Tables          map[string]int64 `json:"tables"`
	SourceDocuments int64            `json:"source_documents"`
	SemanticChunks  int64            `json:"semantic_chunks"`
}

// Dataset is the licence and attribution shown with the data.
type Dataset struct {
	License     string `json:"license"`
	Attribution string `json:"attribution"`
}

// Artifact is one file of a release with its size and SHA-256.
type Artifact struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// Manifest is manifest.json, in the order the builder writes it.
type Manifest struct {
	FormatVersion int         `json:"format_version"`
	ReleaseID     string      `json:"release_id"`
	GeneratedAt   string      `json:"generated_at"`
	AsOfDate      string      `json:"as_of_date,omitempty"`
	Source        Source      `json:"source"`
	Postgres      Postgres    `json:"postgres"`
	TextSearch    *TextSearch `json:"text_search,omitempty"`
	Embedding     Embedding   `json:"embedding"`
	Counts        Counts      `json:"counts"`
	Dataset       Dataset     `json:"dataset"`
	BuildVersion  string      `json:"build_version"`
	Dump          *Artifact   `json:"dump,omitempty"`
}

// Release is a validated manifest: the typed fields, and the document as it
// was read (Raw), which comparisons and receipts use so that fields this
// version does not know are kept.
type Release struct {
	Manifest
	Raw map[string]any
}

// Parse validates a manifest document.
func Parse(b []byte) (*Release, error) {
	var raw map[string]any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&raw); err != nil || raw == nil {
		return nil, ErrManifest
	}
	return FromRaw(raw)
}

// FromRaw validates a decoded manifest (numbers as json.Number or float64).
func FromRaw(raw map[string]any) (*Release, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, ErrManifest
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, ErrManifest
	}
	r := &Release{Manifest: m, Raw: normalize(raw).(map[string]any)}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// normalize turns every number into json.Number, so documents decoded in
// different ways compare equal.
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case float64:
		b, _ := json.Marshal(x)
		return json.Number(b)
	default:
		return v
	}
}

func isInt(v any) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	_, err := n.Int64()
	return err == nil
}

func object(v any) map[string]any { m, _ := v.(map[string]any); return m }

func (r *Release) validate() error {
	raw := r.Raw
	need := func(ok bool) error {
		if !ok {
			return ErrManifest
		}
		return nil
	}
	m := r.Manifest
	checks := []bool{
		isInt(raw["format_version"]) && m.FormatVersion == 1,
		releaseIDPattern.MatchString(m.ReleaseID),
		repoPattern.MatchString(m.Source.RepoID),
		revisionPattern.MatchString(m.Source.Revision),
		Equal(raw["postgres"], map[string]any{"version": "18.6", "vector_version": "0.8.2"}),
	}
	for _, ok := range checks {
		if err := need(ok); err != nil {
			return err
		}
	}
	if ts, ok := raw["text_search"]; ok {
		want := map[string]any{"extension": "pg_trgm", "version": "1.6", "indexes": stringsAny(SearchIndexes)}
		if !Equal(ts, want) {
			return ErrManifest
		}
	}
	emb := object(raw["embedding"])
	if emb == nil {
		return ErrManifest
	}
	core := map[string]any{}
	for k, v := range emb {
		if k != "storage" && k != "provider" {
			core[k] = v
		}
	}
	if !Equal(core, map[string]any{"model": Model, "dimensions": json.Number("768"), "task_type": TaskType,
		"chunk_size": json.Number("900"), "chunk_overlap": json.Number("120")}) {
		return ErrManifest
	}
	if s, ok := emb["storage"]; ok && s != Storage {
		return ErrManifest
	}
	if p, ok := emb["provider"]; ok && p != "gemini" {
		return ErrManifest
	}
	dump := object(raw["dump"])
	if dump == nil || m.Dump == nil {
		return ErrManifest
	}
	if err := SafePath(m.Dump.Path); err != nil {
		return err
	}
	if !isInt(dump["size_bytes"]) || m.Dump.SizeBytes <= 0 || !sha256Pattern.MatchString(m.Dump.SHA256) {
		return ErrManifest
	}
	counts := object(raw["counts"])
	if counts == nil {
		return ErrManifest
	}
	for _, k := range []string{"source_documents", "semantic_chunks"} {
		if !isInt(counts[k]) {
			return ErrManifest
		}
	}
	if m.Counts.SourceDocuments < 0 || m.Counts.SemanticChunks < 0 {
		return ErrManifest
	}
	tables := object(counts["tables"])
	if tables == nil {
		return ErrManifest
	}
	for name, v := range tables {
		if !tablePattern.MatchString(name) || !isInt(v) || m.Counts.Tables[name] < 0 {
			return ErrManifest
		}
	}
	if m.GeneratedAt == "" || m.BuildVersion == "" || m.Dataset.License == "" || m.Dataset.Attribution == "" {
		return ErrManifest
	}
	return nil
}

func stringsAny(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// Equal compares two JSON values, treating every number by its value.
func Equal(a, b any) bool { return reflect.DeepEqual(canon(normalize(a)), canon(normalize(b))) }

// canon makes json.Number comparable by value ("1" and "1.0").
func canon(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = canon(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canon(e)
		}
		return out
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	default:
		return v
	}
}

// External is the manifest without artifact_revision, the field the
// downloader adds to say which dataset commit the files came from.
func (r *Release) External() map[string]any {
	out := make(map[string]any, len(r.Raw))
	for k, v := range r.Raw {
		if k != "artifact_revision" {
			out[k] = v
		}
	}
	return out
}

// Checksum is the SHA-256 of the external manifest as sorted, compact JSON
// with non-ASCII text kept as is: the identity a restore attempt records.
func (r *Release) Checksum() string {
	h := sha256.Sum256(canonicalJSON(r.External()))
	return hex.EncodeToString(h[:])
}

func canonicalJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // map keys are sorted; numbers are json.Number
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// SafePath checks a release-relative path: no absolute paths, backslashes,
// colons or empty, "." and ".." parts.
func SafePath(p string) error {
	if p == "" || strings.ContainsAny(p, `\:`) || strings.HasPrefix(p, "/") {
		return errors.New("invalid artifact path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid artifact path")
		}
	}
	return nil
}

// Load reads and validates a manifest file.
func Load(path string) (*Release, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// ErrChecksum is a file whose size or SHA-256 differs from its manifest.
var ErrChecksum = errors.New("artifact size or checksum mismatch")

// VerifyFile checks a file's size and SHA-256 against a.
func VerifyFile(path string, a Artifact) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if n != a.SizeBytes || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return ErrChecksum
	}
	return nil
}

// Encode writes v as the builder writes manifest.json: two-space indent,
// non-ASCII text as is, and a final newline.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
