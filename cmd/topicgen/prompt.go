package main

import (
	"fmt"
	"strings"
)

const outputShape = `{
  "articles": [ {"id": "a1", "law": 190374, "article": "39", "quote": "<Arabic words copied exactly from that article>"} ],
  "en": {
    "title": "...", "question": "...", "description": "...",
    "answer": {"text": "...", "cites": ["a1"]},
    "sections": [
      {"heading": "<a sub-question in plain words>",
       "paras": [ {"text": "...", "cites": ["a1"]}, {"text": "<what this page does not cover>", "note": true} ],
       "table": {"caption": "...", "head": ["...", "..."], "rows": [ {"cells": ["...", "..."], "cites": ["a1"]} ]} }
    ]
  },
  "ar": { "<the same structure, in Arabic>" }
}`

const rules = `RULES
1. Answer first. "answer" is 2 or 3 sentences that answer the question directly, so a reader who stops there has the answer.
2. 3 to 6 sections. Each heading is one sub-question in plain words. Each section has 1 to 3 short paragraphs (under 60 words each) and, only where the articles hold a list of figures, one table of 3 to 12 rows. Each section must make sense alone: name the thing again instead of writing "as above".
3. Every paragraph that says what the law provides lists, in "cites", the ids of the articles it rests on (1 to 3). A paragraph with no cite must set "note": true and may only say what the page does not cover or where to look; it must not state what the law provides.
4. Quotes. In "articles" write one entry per quoted passage: copy the Arabic words from the article text above exactly, character for character (same spelling, diacritics, spaces and digits). No ellipses, no joining separate parts, no corrections. One to four sentences is typical: the passage that carries the statements which cite it. One entry can serve several paragraphs; one article can have several entries with different passages. Every entry must be cited at least once. Use only the articles above.
5. Numbers. Write every number as digits (15, not fifteen), in both languages. A number may appear only if the cited article states it (in digits or in Arabic words), or it is that article's number or the number or year of its law. Never add, subtract, convert, round or infer a number, and do not state a percentage, amount or period the articles do not state. Give amounts as printed in the article and call them pounds (LBP). The name, number and date of a law may be taken from the headings of the articles above. In Arabic, write one and two as words (شهر، شهران، سنتان، يومان) and use digits from 3 up, so the grammar stays natural.
6. Sources only. Nothing from outside the articles above: no practice, no court rulings, no "usually", no advice such as "you should", no prediction. If an article says another text replaced or amended it, say what the article says. Use the researcher's caveats only where an article supports them, or to warn the reader that something is missing from the database.
7. Plain voice: short sentences, active verbs, figures instead of adjectives, no hype, no filler, no exclamation marks, and no em dashes (use a full stop, a comma or a colon). Name the law and the article in the prose when it helps ("Article 50 of the Labour Code says..."), but never paste Arabic into the English prose.
8. The Arabic version is written for Arabic readers in clear standard Arabic, not word for word from the English. Same content, same structure: same number of sections, paragraphs and rows, and the same cites on each. Digits 0-9.
9. Keep the page short. The English text of the whole page (answer, paragraphs, table cells) stays under 450 words and the Arabic about the same. At most 14 entries in "articles". Quote the passage that carries a statement, not a whole article, and keep each quote under 400 characters (a short article may be quoted whole). Lead each paragraph with its figure or its rule. Within that length be complete: give the whole range the articles give (every category, every period, every condition that changes who is covered), not one example.
10. "title" and "question" are given below: use them as they are in each language. "description" is one sentence under 150 characters saying what the page answers.
11. Reply with one JSON object in exactly the shape below and nothing else: no markdown, no comment.`

// buildPrompt is the one prompt every model gets.
func buildPrompt(s *Spec, arts []Article) string {
	var b strings.Builder
	b.WriteString("You are writing one topic page for LebaneseLegislation (lebaneselegislation.com), a free archive of Lebanese laws. The page answers one question that residents and lawyers ask, in plain words, in English and in Arabic. It is a guide to what the published articles say. It is not advice, and it never states law that the articles below do not state.\n\n")
	b.WriteString("THE QUESTION\n")
	fmt.Fprintf(&b, "Title (EN): %s\nTitle (AR): %s\nQuestion (EN): %s\nQuestion (AR): %s\n", s.TitleEN, s.TitleAR, s.QuestionEN, s.QuestionAR)
	b.WriteString("\nSUB-QUESTIONS TO COVER (merge or drop any the articles cannot answer)\n")
	for _, q := range s.SubQuestion {
		b.WriteString("- " + q + "\n")
	}
	b.WriteString("\nRESEARCHER'S CAVEATS (a pointer, not a source: verify each against the articles)\n" + s.Notes + "\n")
	b.WriteString("\nTHE ARTICLES (the only source; each is the current wording the site shows)\n")
	for _, a := range arts {
		fmt.Fprintf(&b, "\n=== law %d (%s; %s), article %s ===\n%s\n", a.Law, a.Cite, a.Ident, a.Number, a.Text)
	}
	b.WriteString("\n" + rules + "\n\nSHAPE\n" + outputShape + "\n")
	return b.String()
}
