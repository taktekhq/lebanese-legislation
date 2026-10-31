package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// The public dump carries article_calculated_current: Civil Procedure (law
// 244565) amounts multiplied by 50 under decree 330819, as exported by the
// canonical builder. No arithmetic happens here.
const calculatedLaw = 244565

var calculatedAuthority = map[string]any{
	"law_id": float64(330819), "article": "1",
	"url": "https://lebaneselegislation.com/decree/330819/article/1/", "effective_on": "2024-09-19",
}

var calculatedFrom = time.Date(2024, 9, 19, 0, 0, 0, 0, time.UTC)

// calculatedCurrent returns the stored calculated wording for an article when
// the shown text is exactly the in-force source it was calculated from, and
// nil otherwise, so the original text always stands on its own.
func (s *Server) calculatedCurrent(ctx context.Context, lawID int, number, shown string, repealed bool) json.RawMessage {
	if lawID != calculatedLaw || repealed || shown == "" {
		return nil
	}
	var present bool
	if err := s.db.QueryRow(ctx, `SELECT to_regclass('public.article_calculated_current') IS NOT NULL`).Scan(&present); err != nil || !present {
		return nil
	}
	rows, err := s.db.Query(ctx, `SELECT c.source_sha256, c.calculated_current::text FROM article_calculated_current c
		JOIN article_in_force f ON (f.law_id,f.number,f.position)=(c.law_id,c.number,c.position)
		JOIN legislation l ON l.id=c.law_id
		WHERE c.law_id=$1 AND c.number=$2 AND f.text_ar=$3
		AND f.state='in_force' AND f.pending IS FALSE
		AND (f.valid_from IS NULL OR f.valid_from<=CURRENT_DATE)
		AND l.status NOT IN ('repealed','annulled')
		AND (l.issued_on IS NULL OR l.issued_on<=CURRENT_DATE)`, lawID, number, shown)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var found []json.RawMessage
	for rows.Next() {
		var hash, raw string
		if rows.Scan(&hash, &raw) != nil {
			return nil
		}
		if validCalculated(hash, []byte(raw), shown, time.Now()) {
			found = append(found, json.RawMessage(raw))
		}
	}
	if rows.Err() != nil || len(found) != 1 {
		return nil
	}
	return found[0]
}

// validCalculated applies the same checks as the export: scope, authority,
// multiplier and the SHA-256 of the exact source text.
func validCalculated(rowHash string, raw []byte, source string, now time.Time) bool {
	var obj struct {
		Kind               string         `json:"kind"`
		IsPublishedWording *bool          `json:"is_published_wording"`
		Multiplier         *float64       `json:"multiplier"`
		Authority          map[string]any `json:"authority"`
		SourceTextAr       string         `json:"source_text_ar"`
		SourceSHA256       string         `json:"source_sha256"`
		TextAr             string         `json:"text_ar"`
	}
	if json.Unmarshal(raw, &obj) != nil || now.Before(calculatedFrom) {
		return false
	}
	sum := sha256.Sum256([]byte(source))
	digest := hex.EncodeToString(sum[:])
	if obj.Kind != "statutory_monetary_adjustment" || obj.IsPublishedWording == nil || *obj.IsPublishedWording ||
		obj.Multiplier == nil || *obj.Multiplier != 50 || len(obj.Authority) != len(calculatedAuthority) {
		return false
	}
	for k, v := range calculatedAuthority {
		if obj.Authority[k] != v {
			return false
		}
	}
	return obj.SourceTextAr == source && rowHash == digest && obj.SourceSHA256 == digest &&
		strings.TrimSpace(obj.TextAr) != "" && obj.TextAr != source
}
