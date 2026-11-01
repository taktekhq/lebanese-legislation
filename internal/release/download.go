package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/taktekhq/lebanese-legislation/internal/hfhub"
)

// LatestPath is the pointer to the newest release in the dataset.
const LatestPath = "releases/latest.json"

// Pointer is releases/latest.json.
type Pointer struct {
	FormatVersion  int    `json:"format_version"`
	ReleaseID      string `json:"release_id"`
	ManifestPath   string `json:"manifest_path"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// maxMetadata caps a manifest or pointer download.
const maxMetadata = 1 << 20

// Fetch reads the release that manifestPath names in the dataset repo at
// revision: either a manifest, or a pointer (like releases/latest.json)
// whose manifest must match the pointer's SHA-256. It returns the manifest
// with artifact_revision set to the dataset commit, and the manifest's path.
func Fetch(ctx context.Context, hub *hfhub.Client, repo, revision, manifestPath string) (*Release, string, error) {
	sha, err := hub.Revision(ctx, hfhub.Dataset, repo, revision)
	if err != nil {
		return nil, "", fmt.Errorf("release metadata download failed: %w", err)
	}
	if err := SafePath(manifestPath); err != nil {
		return nil, "", err
	}
	raw, err := hub.Read(ctx, hfhub.Dataset, repo, sha, manifestPath, maxMetadata)
	if err != nil {
		return nil, "", fmt.Errorf("release metadata download failed: %w", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil || probe == nil {
		return nil, "", errors.New("invalid release metadata")
	}
	if _, isManifest := probe["dump"]; !isManifest {
		var p Pointer
		if err := json.Unmarshal(raw, &p); err != nil || p.FormatVersion != 1 || SafePath(p.ManifestPath) != nil || !ValidSHA256(p.ManifestSHA256) {
			return nil, "", errors.New("invalid release pointer or manifest checksum")
		}
		manifestPath = p.ManifestPath
		raw, err = hub.Read(ctx, hfhub.Dataset, repo, sha, manifestPath, maxMetadata)
		if err != nil {
			return nil, "", fmt.Errorf("release metadata download failed: %w", err)
		}
		if h := sha256.Sum256(raw); hex.EncodeToString(h[:]) != p.ManifestSHA256 {
			return nil, "", errors.New("invalid release pointer or manifest checksum")
		}
		r, err := Parse(raw)
		if err != nil || r.ReleaseID != p.ReleaseID {
			return nil, "", errors.New("invalid release pointer or manifest checksum")
		}
		r.Raw["artifact_revision"] = sha
		return r, manifestPath, nil
	}
	r, err := Parse(raw)
	if err != nil {
		return nil, "", err
	}
	r.Raw["artifact_revision"] = sha
	return r, manifestPath, nil
}

// ArtifactRevision is the dataset commit Fetch read the release at.
func (r *Release) ArtifactRevision() string { s, _ := r.Raw["artifact_revision"].(string); return s }

// Download fetches a release's dump into cacheDir/<commit>/, verifying its
// size and SHA-256, and returns the local path. A file already there is
// verified and reused. A failed download leaves no partial file.
func Download(ctx context.Context, hub *hfhub.Client, repo string, r *Release, manifestPath, cacheDir string) (string, error) {
	sha := r.ArtifactRevision()
	if !ValidRevision(sha) {
		return "", errors.New("release has no dataset commit")
	}
	root, err := filepath.Abs(filepath.Join(cacheDir, sha))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	artifact := path.Join(path.Dir(manifestPath), r.Dump.Path)
	name := sha256.Sum256([]byte(artifact))
	target := filepath.Join(root, hex.EncodeToString(name[:]))
	if _, err := os.Stat(target); err == nil {
		return target, VerifyFile(target, *r.Dump)
	}
	tmp, err := os.CreateTemp(root, "download-*.partial")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	res, err := hub.Open(ctx, hfhub.Dataset, repo, sha, artifact, "")
	if err != nil {
		return "", fmt.Errorf("artifact download failed: %w", err)
	}
	defer res.Body.Close()
	n, err := io.Copy(tmp, io.LimitReader(res.Body, r.Dump.SizeBytes+1))
	if err != nil {
		return "", fmt.Errorf("artifact download failed: %w", err)
	}
	if n > r.Dump.SizeBytes {
		return "", errors.New("artifact exceeds declared size")
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := VerifyFile(tmp.Name(), *r.Dump); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return "", err
	}
	keep = true
	return target, nil
}
