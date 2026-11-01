package hfhub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
)

// File is one file to add in a commit: Content in memory, or the file at
// Path on disk. LFS stores it through Git LFS (large or binary files);
// otherwise it is sent inline.
type File struct {
	PathInRepo string
	Content    []byte
	Path       string
	LFS        bool
}

// Commit is one atomic change to a branch.
type Commit struct {
	Branch  string // e.g. "main"
	Parent  string // the commit the change is based on; the Hub refuses it if the branch moved
	Message string
	Files   []File
}

type lfsObject struct {
	file   *File
	sha256 string
	size   int64
}

func (f *File) open() (io.ReadSeekCloser, int64, error) {
	if f.Path == "" {
		return nopCloser{bytes.NewReader(f.Content)}, int64(len(f.Content)), nil
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		return nil, 0, err
	}
	st, err := fh.Stat()
	if err != nil {
		fh.Close()
		return nil, 0, err
	}
	return fh, st.Size(), nil
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func digest(f *File) (string, int64, error) {
	r, size, err := f.open()
	if err != nil {
		return "", 0, err
	}
	defer r.Close()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

// CreateCommit uploads the LFS files the Hub does not have yet, then commits
// every file in one commit, and returns the new commit's id.
func (c *Client) CreateCommit(ctx context.Context, t RepoType, repo string, commit Commit) (string, error) {
	if !ValidRepoID(repo) {
		return "", errors.New("hub: invalid repository id")
	}
	if c.Token == "" {
		return "", errors.New("hub: a token is required to commit")
	}
	branch := commit.Branch
	if branch == "" {
		branch = "main"
	}
	var objects []lfsObject
	for i := range commit.Files {
		f := &commit.Files[i]
		if !f.LFS {
			continue
		}
		sum, size, err := digest(f)
		if err != nil {
			return "", err
		}
		objects = append(objects, lfsObject{file: f, sha256: sum, size: size})
	}
	if len(objects) > 0 {
		if err := c.uploadLFS(ctx, t, repo, branch, objects); err != nil {
			return "", err
		}
	}
	var body bytes.Buffer
	enc := json.NewEncoder(&body)
	header := map[string]any{"summary": commit.Message, "description": ""}
	if commit.Parent != "" {
		header["parentCommit"] = commit.Parent
	}
	if err := enc.Encode(map[string]any{"key": "header", "value": header}); err != nil {
		return "", err
	}
	byFile := map[*File]lfsObject{}
	for _, o := range objects {
		byFile[o.file] = o
	}
	for i := range commit.Files {
		f := &commit.Files[i]
		if o, ok := byFile[f]; ok {
			if err := enc.Encode(map[string]any{"key": "lfsFile", "value": map[string]any{
				"path": f.PathInRepo, "algo": "sha256", "oid": o.sha256, "size": o.size}}); err != nil {
				return "", err
			}
			continue
		}
		content := f.Content
		if f.Path != "" {
			b, err := os.ReadFile(f.Path)
			if err != nil {
				return "", err
			}
			content = b
		}
		if err := enc.Encode(map[string]any{"key": "file", "value": map[string]any{
			"path": f.PathInRepo, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(content)}}); err != nil {
			return "", err
		}
	}
	u := c.api(t, repo, "commit", url.PathEscape(branch))
	payload := body.Bytes()
	res, err := c.do(ctx, http.MethodPost, u, http.Header{"Content-Type": {"application/x-ndjson"}},
		func() io.Reader { return bytes.NewReader(payload) }, false)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if err := check(res, http.MethodPost, u); err != nil {
		return "", err
	}
	var out struct {
		CommitOID string `json:"commitOid"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("hub: commit response: %w", err)
	}
	if out.CommitOID == "" {
		return "", errors.New("hub: commit response has no commit id")
	}
	return out.CommitOID, nil
}

type lfsAction struct {
	Href   string            `json:"href"`
	Header map[string]string `json:"header"`
}

type lfsBatchResponse struct {
	Transfer string `json:"transfer"`
	Objects  []struct {
		OID     string               `json:"oid"`
		Size    int64                `json:"size"`
		Actions map[string]lfsAction `json:"actions"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"objects"`
}

var lfsHeaders = http.Header{
	"Accept":       {"application/vnd.git-lfs+json"},
	"Content-Type": {"application/vnd.git-lfs+json"},
}

func (c *Client) lfsBatchURL(t RepoType, repo string) string {
	prefix := ""
	if t != Model {
		prefix = "/" + string(t) + "s"
	}
	return c.Endpoint + prefix + "/" + repo + ".git/info/lfs/objects/batch"
}

func (c *Client) uploadLFS(ctx context.Context, t RepoType, repo, branch string, objects []lfsObject) error {
	req := map[string]any{"operation": "upload", "transfers": []string{"basic", "multipart"},
		"hash_algo": "sha256", "ref": map[string]string{"name": "refs/heads/" + branch}}
	var objs []map[string]any
	bySHA := map[string]lfsObject{}
	for _, o := range objects {
		objs = append(objs, map[string]any{"oid": o.sha256, "size": o.size})
		bySHA[o.sha256] = o
	}
	req["objects"] = objs
	body, err := jsonBody(req)
	if err != nil {
		return err
	}
	u := c.lfsBatchURL(t, repo)
	res, err := c.do(ctx, http.MethodPost, u, lfsHeaders, body, true)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := check(res, http.MethodPost, u); err != nil {
		return err
	}
	var batch lfsBatchResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&batch); err != nil {
		return fmt.Errorf("hub: LFS batch response: %w", err)
	}
	for _, ob := range batch.Objects {
		if ob.Error != nil {
			return fmt.Errorf("hub: LFS refused %s: %d %s", ob.OID, ob.Error.Code, ob.Error.Message)
		}
		o, ok := bySHA[ob.OID]
		if !ok {
			return fmt.Errorf("hub: LFS answered for an unknown object %s", ob.OID)
		}
		upload, ok := ob.Actions["upload"]
		if !ok {
			continue // the Hub already has it
		}
		if _, multipart := upload.Header["chunk_size"]; multipart || batch.Transfer == "multipart" {
			err = c.uploadMultipart(ctx, o, upload)
		} else {
			err = c.uploadBasic(ctx, o, upload)
		}
		if err != nil {
			return err
		}
		if verify, ok := ob.Actions["verify"]; ok {
			if err := c.lfsVerify(ctx, o, verify); err != nil {
				return err
			}
		}
	}
	return nil
}

func actionHeader(a lfsAction) http.Header {
	h := http.Header{}
	for k, v := range a.Header {
		h.Set(k, v)
	}
	return h
}

func (c *Client) uploadBasic(ctx context.Context, o lfsObject, a lfsAction) error {
	r, size, err := o.file.open()
	if err != nil {
		return err
	}
	defer r.Close()
	h := actionHeader(a)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	res, err := c.send(ctx, http.MethodPut, a.Href, h, func() io.Reader {
		_, _ = r.Seek(0, io.SeekStart)
		return io.NopCloser(r)
	}, true, false)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return check(res, http.MethodPut, a.Href)
}

func (c *Client) uploadMultipart(ctx context.Context, o lfsObject, a lfsAction) error {
	chunk, err := strconv.ParseInt(a.Header["chunk_size"], 10, 64)
	if err != nil || chunk <= 0 {
		return errors.New("hub: LFS multipart upload has no valid chunk_size")
	}
	type part struct {
		n   int
		url string
	}
	var parts []part
	for k, v := range a.Header {
		if n, err := strconv.Atoi(k); err == nil && n > 0 {
			parts = append(parts, part{n, v})
		}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].n < parts[j].n })
	if want := (o.size + chunk - 1) / chunk; int64(len(parts)) != want {
		return fmt.Errorf("hub: LFS multipart upload has %d part URLs, want %d", len(parts), want)
	}
	r, _, err := o.file.open()
	if err != nil {
		return err
	}
	defer r.Close()
	var done []map[string]any
	for i, p := range parts {
		offset := int64(i) * chunk
		length := min(chunk, o.size-offset)
		res, err := c.send(ctx, http.MethodPut, p.url, http.Header{"Content-Length": {strconv.FormatInt(length, 10)}}, func() io.Reader {
			return io.NewSectionReader(readerAt{r}, offset, length)
		}, true, false)
		if err != nil {
			return err
		}
		err = check(res, http.MethodPut, p.url)
		etag := res.Header.Get("ETag")
		res.Body.Close()
		if err != nil {
			return err
		}
		if etag == "" {
			return fmt.Errorf("hub: LFS part %d returned no ETag", p.n)
		}
		done = append(done, map[string]any{"partNumber": i + 1, "etag": etag})
	}
	body, err := jsonBody(map[string]any{"oid": o.sha256, "parts": done})
	if err != nil {
		return err
	}
	res, err := c.send(ctx, http.MethodPost, a.Href, lfsHeaders.Clone(), body, true, false)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return check(res, http.MethodPost, a.Href)
}

// readerAt adapts a ReadSeeker (an *os.File or bytes.Reader both are
// ReaderAt) for SectionReader.
type readerAt struct{ r io.ReadSeeker }

func (ra readerAt) ReadAt(p []byte, off int64) (int, error) {
	if at, ok := ra.r.(io.ReaderAt); ok {
		return at.ReadAt(p, off)
	}
	if b, ok := ra.r.(nopCloser); ok {
		return b.ReadAt(p, off)
	}
	return 0, errors.New("hub: file cannot be read at an offset")
}

func (c *Client) lfsVerify(ctx context.Context, o lfsObject, a lfsAction) error {
	body, err := jsonBody(map[string]any{"oid": o.sha256, "size": o.size})
	if err != nil {
		return err
	}
	h := actionHeader(a)
	for k, v := range lfsHeaders {
		h[k] = v
	}
	// The verify endpoint takes the token as a basic-auth password.
	h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("USER:"+c.Token)))
	res, err := c.send(ctx, http.MethodPost, a.Href, h, body, true, false)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return check(res, http.MethodPost, a.Href)
}
