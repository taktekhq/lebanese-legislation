package cite

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taktekhq/lebanese-legislation/internal/amend"
)

// Agy reads articles through the Antigravity CLI (`agy`), which runs Gemini
// on a Google AI subscription instead of the paid API. It reads a batch of
// articles per call: the CLI is slow to start and runs one or two calls at a
// time. Same prompt, same checks, same file as the API path.
type Agy struct {
	Remote    string        // run agy on this ssh host instead (its PATH must have agy)
	RemoteBin string        // the command on that host ("agy" when empty), e.g. a wrapper for another login
	Bin       string        // path to agy
	Model     string        // e.g. "Gemini 3.8 Flash (Low)"
	Timeout   time.Duration // per call
}

func (it Item) batchPart() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### key: %s\nThe text: %s\nIts title: %s\n", it.Key, it.Identity, it.Title)
	if it.Before != "" {
		before := []rune(it.Before)
		if len(before) > 1200 {
			before = before[len(before)-1200:]
		}
		fmt.Fprintf(&b, "The articles before it, for context only:\n%s\n", string(before))
	}
	fmt.Fprintf(&b, "The article to read (article %s):\n%s\n\n", it.Article, it.Text)
	return b.String()
}

var amendVerb = regexp.MustCompile(`(تعدل|يعدل|عدلت|تستبدل|يستبدل|تلغي|يلغي|تضاف|يضاف)`)

const batchFormat = `Answer for every key above. Output format: a single JSON object {"items":[{"key":"<the key>","amends":true|false,"changes":[{"action":"...","kind":"...","number":"","date":"","year":0,"name":"","self":false,"article":"","part":"","quote":"","why":""}]}]}. action is one of: replace, amend, repeal, insert, suspend, extend, general_repeal, cites, applies, derogates, note_amended_by, note_repealed_by, note_added_by, other. kind is one of: law, decree-law, decree, decision, law-by-decree, constitution, unknown. Do not use any tools. Print only the JSON.`

type batchAnswer struct {
	Items []struct {
		Key string `json:"key"`
		answer
	} `json:"items"`
}

// batchPrompt is the prompt for one batch of articles.
func batchPrompt(items []Item) string { return Articles.prompt(items) }

// verbFirst puts the articles with an amending verb first: they carry the
// changes.
func verbFirst(todo []Item) {
	verb := func(it Item) bool { return amendVerb.MatchString(amend.FoldString(it.Text)) }
	sort.SliceStable(todo, func(i, j int) bool { return verb(todo[i]) && !verb(todo[j]) })
}

// parseBatch reads the model's answer to a batch: the JSON object in it, with
// or without a code fence or a word around it.
func parseBatch(response string) (map[string]answer, error) {
	resp := strings.TrimSpace(response)
	if resp == "" {
		return nil, fmt.Errorf("empty answer")
	}
	obj := firstJSON.FindString(resp)
	if obj == "" {
		return nil, fmt.Errorf("answer has no JSON: %.200s", resp)
	}
	var ba batchAnswer
	if err := json.Unmarshal([]byte(obj), &ba); err != nil {
		return nil, fmt.Errorf("answer: %v", err)
	}
	got := map[string]answer{}
	for _, x := range ba.Items {
		got[x.Key] = x.answer
	}
	return got, nil
}

// agyProc is one long-lived agy session: booting agy takes 13 to 19 s, a
// prompt to a running session about 2 s. It reads prompts as stream-json
// lines on stdin and answers each with a "result" event. One session is one
// conversation, so it is recycled after a few prompts.
type agyProc struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	dir  string
	uses int
}

func (a *Agy) start() (*agyProc, error) {
	args := []string{"--model", a.Model, "--input-format", "stream-json", "--output-format", "stream-json", "-p", ""}
	var cmd *exec.Cmd
	var dir string
	if a.Remote != "" {
		// On another machine over ssh: an empty folder there, agy from its PATH.
		quoted := make([]string, len(args))
		for i, x := range args {
			quoted[i] = "'" + strings.ReplaceAll(x, "'", `'\''`) + "'"
		}
		cmd = exec.Command("ssh", "-o", "BatchMode=yes", a.Remote,
			`export PATH="$HOME/.local/bin:$PATH"; cd "$(mktemp -d)" && exec `+remoteBin(a.RemoteBin)+` `+strings.Join(quoted, " "))
	} else {
		var err error
		if dir, err = os.MkdirTemp("", "cite-agy-"); err != nil {
			return nil, err
		}
		cmd = exec.Command(a.Bin, args...)
		cmd.Dir = dir // an empty folder: nothing for the agent to look at
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &agyProc{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20), dir: dir}, nil
}

func remoteBin(b string) string {
	if b == "" {
		return "agy"
	}
	return b
}

func (p *agyProc) stop() {
	p.in.Close()
	if p.cmd.Process != nil {
		p.cmd.Process.Kill()
	}
	p.cmd.Wait()
	if p.dir != "" {
		os.RemoveAll(p.dir)
	}
}

// prompt sends one prompt and waits for its result, or kills the session
// after timeout.
func (p *agyProc) prompt(text string, timeout time.Duration) (string, error) {
	line, _ := json.Marshal(map[string]any{"event": "user", "message": map[string]any{"role": "user", "content": text}})
	if _, err := p.in.Write(append(line, '\n')); err != nil {
		return "", fmt.Errorf("agy: write: %v", err)
	}
	p.uses++
	type res struct {
		resp string
		err  error
	}
	done := make(chan res, 1)
	go func() {
		for {
			l, err := p.out.ReadBytes('\n')
			if err != nil {
				done <- res{err: fmt.Errorf("agy: session ended: %v", err)}
				return
			}
			var ev struct {
				Event  string `json:"event"`
				Result struct {
					Status   string `json:"status"`
					Response string `json:"response"`
				} `json:"result"`
			}
			if json.Unmarshal(l, &ev) != nil || ev.Event != "result" {
				continue
			}
			if ev.Result.Status != "SUCCESS" {
				done <- res{err: fmt.Errorf("agy: status %s", ev.Result.Status)}
				return
			}
			done <- res{resp: ev.Result.Response}
			return
		}
	}()
	select {
	case r := <-done:
		return r.resp, r.err
	case <-time.After(timeout):
		p.stop() // unblocks the reader
		return "", fmt.Errorf("agy: no answer in %s", timeout)
	}
}

// maxUses recycles a session before its conversation grows too long.
const maxUses = 12

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}
