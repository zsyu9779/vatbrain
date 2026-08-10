package embedder

import (
	"context"
	"regexp"
	"strings"
)

// EntityRefRe finds code-entity references in a question — file-like paths
// with a known code extension, optionally @-prefixed. It is the shared
// entity heuristic of query expansion and the provider's pitfall recall
// (one pattern, no drift). Case-insensitive so "CLAWFEED-PUSH-V3.PY" and
// "clawfeed-push-v3.py" expand to the same canonical term.
var EntityRefRe = regexp.MustCompile(`(?i)@?[\w./-]+\.(go|proto|ts|tsx|js|py|java|rs)`)

// latinTokenRe finds Latin word tokens and numeric runs. CJK text needs no
// tokenization — its character bigrams are already the keyword features.
var latinTokenRe = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9]*|\d{2,}`)

// ExpandQuery expands a retrieval question into its keyword/entity form:
// the original question followed by its deterministic terms — code-entity
// references (canonical lowercase), Latin word tokens, and numeric runs —
// reusing the keyword channel's CJK-safe feature machinery. It needs no
// external service: same question always yields the same expansion.
//
// The expanded text feeds both retrieval channels: the semantic embedder
// sees the canonical entity anchors, and the lexical channel (BigramOverlap)
// scores its bigram set against candidate summaries. Scoring is set-based,
// so appended terms never change the keyword-channel vector direction of the
// question itself; the recall lever is the ASCII case folding applied by
// CharBigrams on both sides.
func ExpandQuery(ctx context.Context, question string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return "", nil
	}

	var terms []string
	seen := make(map[string]struct{})
	add := func(term string) {
		term = strings.ToLower(term)
		if _, ok := seen[term]; ok {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	for _, ref := range EntityRefRe.FindAllString(question, -1) {
		add(strings.TrimPrefix(ref, "@"))
	}
	for _, tok := range latinTokenRe.FindAllString(question, -1) {
		add(tok)
	}
	if len(terms) == 0 {
		return question, nil
	}
	return question + " " + strings.Join(terms, " "), nil
}

// CharBigrams returns the character-bigram set of s, with ASCII letters
// folded to lowercase before extraction so Latin case variants ("OpenClash"
// vs "openclash") produce the same set; CJK characters are unaffected. This
// is the keyword channel's feature representation, shared by the lexical
// fallback and the RRF lexical ranking.
func CharBigrams(s string) map[string]struct{} {
	s = strings.ToLower(s)
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	if len(r) == 1 {
		return map[string]struct{}{string(r[0]): {}}
	}
	out := make(map[string]struct{}, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = struct{}{}
	}
	return out
}

// BigramOverlap returns the Dice coefficient of the character-bigram sets of
// a and b: 1.0 for identical text, 0 for no shared bigram, strictly between
// for partial overlap. CJK-safe and ASCII case-insensitive. Empty input
// scores 0.
func BigramOverlap(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	return BigramOverlapFromSets(CharBigrams(a), CharBigrams(b))
}

// BigramOverlapFromSets computes the Dice coefficient of two precomputed
// bigram sets.
func BigramOverlapFromSets(ab, bb map[string]struct{}) float64 {
	if len(ab) == 0 || len(bb) == 0 {
		return 0
	}
	inter := 0
	for g := range ab {
		if _, ok := bb[g]; ok {
			inter++
		}
	}
	return 2.0 * float64(inter) / float64(len(ab)+len(bb))
}
