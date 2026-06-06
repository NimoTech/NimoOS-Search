package fileindex

import (
	"strings"
	"unicode"
)

// tokenize lowercases and splits on whitespace, _ , - and camelCase boundaries.
func tokenize(q string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	var prev rune
	for _, r := range q {
		switch {
		case r == '_' || r == '-' || unicode.IsSpace(r):
			flush()
		case unicode.IsUpper(r) && prev != 0 && !unicode.IsUpper(prev):
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
		prev = r
	}
	flush()
	return out
}

// scoreName returns 0 if no term matches, else a relevance score: +1 per term
// hit, +0.5 if the whole joined query is a substring, +0.5 if any term is a
// prefix of the name.
func scoreName(nameLower string, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}
	var score float64
	hits := 0
	for _, t := range terms {
		if strings.Contains(nameLower, t) {
			hits++
			score += 1
			if strings.HasPrefix(nameLower, t) {
				score += 0.5
			}
		}
	}
	if hits == 0 {
		return 0
	}
	if strings.Contains(nameLower, strings.Join(terms, " ")) {
		score += 0.5
	}
	return score
}
