// Package judge asks a model (Gemini) about the amendments the patterns
// couldn't place: does the Centre's text already carry the change, predate
// it, or carry a later wording? The model only judges; it never writes the
// law's wording. Its answers are kept as data (db/model/judgements.jsonl)
// for the build to apply: the build never calls a model, stays offline and
// gives the same database every night, and every decision stays in git with
// the model and prompt that made it.
package judge

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Judgement is a model's answer about one change to one article.
type Judgement struct {
	Key    string `json:"key"`    // the change: amending text/article/seq/amended article
	Input  string `json:"input"`  // hash of what the model was shown; stale when it differs
	Task   string `json:"task"`   // "state"
	Prompt string `json:"prompt"` // prompt version
	Model  string `json:"model"`  // the model version that answered
	// State of the Centre's text against the change: "includes" (already
	// carries it), "predates" (is older), "later" (has a later wording),
	// "unclear".
	State  string `json:"state"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

// Key names a change the way the build and the judge both can.
func Key(byLaw int, byArticle string, seq int, article string) string {
	return fmt.Sprintf("%d/%s/%d/%s", byLaw, byArticle, seq, article)
}

// InputHash fingerprints what a judgement depends on: the amending article,
// the Centre's text of the amended article, and the part named.
func InputHash(amending, centre, article, part string) string {
	h := sha256.Sum256([]byte(amending + "\x00" + centre + "\x00" + article + "\x00" + part))
	return hex.EncodeToString(h[:8])
}

// Parse reads judgements, the last for each key winning.
func Parse(data []byte) (map[string]Judgement, error) {
	out := map[string]Judgement{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var j Judgement
		if err := json.Unmarshal([]byte(line), &j); err != nil {
			return nil, err
		}
		out[j.Key] = j
	}
	return out, sc.Err()
}

// Load reads a judgements file; a missing file is empty.
func Load(path string) (map[string]Judgement, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]Judgement{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Save writes judgements sorted by key, one JSON object a line, so git
// diffs show exactly which decisions changed.
func Save(path string, js map[string]Judgement) error {
	keys := make([]string, 0, len(js))
	for k := range js {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		line, err := json.Marshal(js[k])
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
