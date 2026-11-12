package site

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// An A2A endpoint (Agent2Agent, a2a-protocol.org) at /a2a: JSON-RPC 2.0, one
// message in and one message out. The message's text is searched the way
// /api/v1/search/ searches it and the reply lists what matched, as text and
// as the same JSON the API returns. There is no language model here and no
// task to follow: an agent that wants an article's wording reads it from the
// API or the MCP server, which the reply's links and the agent card
// (/.well-known/agent-card.json, agents.go) point to.
//
// It answers message/send (A2A 0.3) and SendMessage (A2A 1.0).

const (
	a2aMaxBody  = 64 * 1024
	a2aPerPart  = 5   // results of each kind in a reply
	a2aMaxRunes = 199 // the search reads questions shorter than 200 characters
)

func (s *Server) a2aRoutes() {
	if s.isStatic {
		return
	}
	s.mux.HandleFunc("POST /a2a", s.a2aPost)
	s.mux.HandleFunc("OPTIONS /a2a", func(w http.ResponseWriter, r *http.Request) {
		a2aCORS(w)
		w.WriteHeader(http.StatusNoContent)
	})
	s.mux.HandleFunc("GET /a2a", func(w http.ResponseWriter, r *http.Request) {
		a2aCORS(w)
		w.Header().Set("Allow", "POST, OPTIONS")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"name": agentsName + " lookup (A2A)", "use": "POST JSON-RPC 2.0 to this address: message/send or SendMessage",
			"card": Base + "/.well-known/agent-card.json"})
	})
}

func a2aCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, A2A-Version")
}

// memResponse holds a handler's answer in memory.
type memResponse struct {
	header http.Header
	code   int
	body   bytes.Buffer
}

func (m *memResponse) Header() http.Header         { return m.header }
func (m *memResponse) WriteHeader(code int)        { m.code = code }
func (m *memResponse) Write(b []byte) (int, error) { return m.body.Write(b) }

func (s *Server) a2aPost(w http.ResponseWriter, r *http.Request) {
	a2aCORS(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if wait, ok := s.ipLim.Allow(clientIP(r)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeRPC(w, http.StatusTooManyRequests, rpcErrorResponse(nil, rpcInvalidRequest, "too many requests, slow down"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, a2aMaxBody))
	if err != nil {
		writeRPC(w, http.StatusRequestEntityTooLarge, rpcErrorResponse(nil, rpcInvalidRequest, "request body too large"))
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, http.StatusOK, rpcErrorResponse(nil, rpcParseError, "invalid JSON"))
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeRPC(w, http.StatusOK, rpcErrorResponse(req.ID, rpcInvalidRequest, "not a JSON-RPC 2.0 request"))
		return
	}
	if isNotification(req.ID) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	v1 := req.Method == "SendMessage"
	if !v1 && req.Method != "message/send" {
		writeRPC(w, http.StatusOK, rpcErrorResponse(req.ID, rpcMethodNotFound,
			"unknown method "+req.Method+": this agent answers message/send (A2A 0.3) and SendMessage (A2A 1.0) only"))
		return
	}
	var p struct {
		Message struct {
			ContextID string `json:"contextId"`
			Parts     []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"message"`
	}
	_ = json.Unmarshal(req.Params, &p)
	var words []string
	for _, part := range p.Message.Parts {
		words = append(words, strings.Fields(part.Text)...)
	}
	q := strings.Join(words, " ")
	if q == "" {
		writeRPC(w, http.StatusOK, rpcErrorResponse(req.ID, rpcInvalidParams, "send a message with a text part: a question or a few words about Lebanese law"))
		return
	}
	if rs := []rune(q); len(rs) > a2aMaxRunes {
		writeRPC(w, http.StatusOK, rpcErrorResponse(req.ID, rpcInvalidParams, "the question is too long: keep it under 200 characters"))
		return
	}

	// The API's own search, asked in process: the same rescue of a question
	// that finds nothing, the same ranking, the same JSON. It runs up to four
	// queries at once, so it waits for a database slot as /api/v1/ and /mcp
	// do (apiLimited, api.go): a burst here must not take the pool from the
	// pages.
	release, ok := s.dbSem.acquire(r.Context(), apiSemaphoreWait)
	if !ok {
		w.Header().Set("Retry-After", "2")
		writeRPC(w, http.StatusServiceUnavailable, rpcErrorResponse(req.ID, rpcInternalError, "the server is busy, try again shortly"))
		return
	}
	defer release()
	sub := r.Clone(r.Context())
	sub.Method, sub.Body = http.MethodGet, http.NoBody
	sub.URL = &url.URL{Path: "/api/v1/search/", RawQuery: url.Values{"q": {q}, "limit": {strconv.Itoa(a2aPerPart)}}.Encode()}
	rec := &memResponse{header: http.Header{}, code: http.StatusOK}
	s.apiSearch(rec, sub)
	var found apiSearchResult
	if rec.code != http.StatusOK || json.Unmarshal(rec.body.Bytes(), &found) != nil {
		writeRPC(w, http.StatusOK, rpcErrorResponse(req.ID, rpcInternalError, "the search is busy, try again shortly"))
		return
	}
	text := a2aText(q, found)
	data := json.RawMessage(bytes.TrimSpace(rec.body.Bytes()))
	id := a2aMessageID()
	var result map[string]any
	if v1 {
		msg := map[string]any{"messageId": id, "role": "ROLE_AGENT", "parts": []map[string]any{{"text": text}, {"data": data}}}
		if p.Message.ContextID != "" {
			msg["contextId"] = p.Message.ContextID
		}
		result = map[string]any{"message": msg}
	} else {
		result = map[string]any{"kind": "message", "messageId": id, "role": "agent",
			"parts": []map[string]any{{"kind": "text", "text": text}, {"kind": "data", "data": data}}}
		if p.Message.ContextID != "" {
			result["contextId"] = p.Message.ContextID
		}
	}
	writeRPC(w, http.StatusOK, rpcResultResponse(req.ID, result))
}

func a2aMessageID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80 // a version 4 UUID
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// a2aLaw is one text on a line: its Arabic title, the unofficial English one,
// kind, number, date and status.
func a2aLaw(l apiLawSummary) string {
	parts := []string{l.Title}
	if en := deref(l.TitleEn); en != "" {
		parts[0] += " (" + en + ", unofficial English title)"
	}
	kind := l.Kind
	if en := deref(l.KindEn); en != "" {
		kind = en
	}
	if n := deref(l.Number); n != "" {
		kind += " No. " + n
	}
	parts = append(parts, kind)
	if d := deref(l.IssuedOn); d != "" {
		parts = append(parts, d)
	}
	return strings.Join(append(parts, strings.ReplaceAll(l.Status, "_", " ")), " · ")
}

// a2aText is the reply a person or a model reads: what matched, with links.
func a2aText(q string, f apiSearchResult) string {
	var b strings.Builder
	if len(f.Laws)+len(f.Articles)+len(f.Rulings)+len(f.Gazette) == 0 {
		fmt.Fprintf(&b, "No Lebanese text matches %q. Try fewer words, the Arabic term, or a citation such as \"المادة 50 من قانون العمل\". Browse: %s/codes/ . Search as data: %s/api/v1/search/?q=\n\n%s\n", q, Base, Base, notLegalAdvice)
		return b.String()
	}
	fmt.Fprintf(&b, "Lebanese law matching %q. This is a search of the published texts, with no language model: read the text at each link before relying on it.\n", q)
	if f.SearchedInstead != "" {
		fmt.Fprintf(&b, "Nothing matched the words as written, so these are the results for %q.\n", f.SearchedInstead)
	}
	if len(f.Articles) > 0 {
		fmt.Fprintf(&b, "\nArticles (%d found)\n", f.ArtCount)
		for i, a := range f.Articles {
			fmt.Fprintf(&b, "%d. Article %s, %s\n   %s\n", i+1, a.Number, a2aLaw(a.Law), a.URL)
			if a.Snippet != "" {
				fmt.Fprintf(&b, "   %s\n", strings.Join(strings.Fields(a.Snippet), " "))
			}
		}
	}
	if len(f.Laws) > 0 {
		fmt.Fprintf(&b, "\nLaws, decrees and decisions (%d found)\n", f.LawCount)
		for i, l := range f.Laws {
			fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, a2aLaw(l), l.URL)
		}
	}
	if len(f.Rulings) > 0 {
		fmt.Fprintf(&b, "\nCourt rulings (%d found)\n", f.RulCount)
		for i, r := range f.Rulings {
			line := r.Title
			for _, x := range []string{r.Court, r.DecidedOn} {
				if x != "" {
					line += " · " + x
				}
			}
			fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, line, r.URL)
		}
	}
	if len(f.Gazette) > 0 {
		fmt.Fprintf(&b, "\nOfficial Gazette issues (%d found)\n", f.GazCount)
		for i, g := range f.Gazette {
			line := "Issue " + g.Number
			if g.PublishedOn != "" {
				line += " · " + g.PublishedOn
			}
			fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, line, g.URL)
		}
	}
	fmt.Fprintf(&b, "\nMore results and exact wording: %s/api/v1/search/?q=%s , or the MCP server at %s/mcp .\n%s\n", Base, url.QueryEscape(q), Base, notLegalAdvice)
	return b.String()
}
