package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/vatbrain/vatbrain/internal/core"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
	"github.com/vatbrain/vatbrain/internal/vector"
)

// retrieveLimit bounds the candidate pools and final results for prefetch so
// the hot path stays under the 200ms budget.
const (
	episodicCandidatePool = 500
	episodicResultLimit   = 5
	pitfallCandidatePool  = 50
	pitfallResultLimit    = 3
	// surpriseRankingBoost is a conservative SurpriseBoost applied on the live
	// recall path: it keeps ordering predominantly semantic while letting a
	// high-surprise memory (a past correction) edge out an otherwise-equal
	// peer. 0 would disable the prediction-error signal from ranking entirely.
	surpriseRankingBoost = 0.25
)

// entityRefRe finds code-entity references in a query (mirrors the refiner's
// heuristic so entity-anchored pitfalls can be surfaced from the query text).
var entityRefRe = regexp.MustCompile(`@?[\w./-]+\.(go|proto|ts|tsx|js|py|java|rs)`)

// PrefetchContext is the plain-text recall bundle returned to hermes. The
// hermes MemoryManager wraps it in the <memory-context> fence — providers
// never emit the fence themselves (§5.3).
type PrefetchContext struct {
	Episodes []models.EpisodicMemory
	Pitfalls []models.PitfallMemory
}

// RetrieveEpisodic finds the episodic memories most relevant to query within
// a project. Uses embedding cosine when the embedder yields a signal
// (CJK-safe, per F1); otherwise a character-bigram overlap score that also
// handles Chinese. Relative-time expressions in the query ("上周", "last
// week", "最近一次", "most recent") narrow the result to the implied
// occurred_at window and/or order results newest-first (ParseRelativeTime).
func RetrieveEpisodic(ctx context.Context, deps core.WriteDeps, projectID, query string, limit int) ([]models.EpisodicMemory, error) {
	if limit <= 0 {
		limit = episodicResultLimit
	}

	window := ParseRelativeTime(query, time.Now())

	emb, err := deps.Embedder.Embed(ctx, query)
	if err == nil && hasVectorMagnitude(emb) {
		return deps.Store.SearchEpisodic(ctx, store.EpisodicSearchRequest{
			ProjectID:        projectID,
			Embedding:        vector.Float32To64(emb),
			Limit:            limit,
			SurpriseBoost:    surpriseRankingBoost,
			OccurredAfter:    window.After,
			OccurredBefore:   window.Before,
			SortByOccurredAt: window.SortNewest,
		})
	}

	// Lexical fallback: score recent episodes by query overlap. The temporal
	// window narrows the pool (ScanRecent carries occurred_at) and, for
	// "最近一次" queries, the relevant top-k are ordered by occurred_at
	// descending — the same rank-then-sort semantics as the embedding path.
	items, err := deps.Store.ScanRecent(ctx, time.Time{}, episodicCandidatePool)
	if err != nil {
		return nil, err
	}

	type scored struct {
		mem   models.EpisodicMemory
		score float64
	}
	var pool []scored
	seen := make(map[string]struct{})
	for _, item := range items {
		if projectID != "" && item.ProjectID != projectID {
			continue
		}
		if !window.After.IsZero() && item.OccurredAt.Before(window.After) {
			continue
		}
		if !window.Before.IsZero() && item.OccurredAt.After(window.Before) {
			continue
		}
		if _, ok := seen[item.ID.String()]; ok {
			continue
		}
		seen[item.ID.String()] = struct{}{}
		pool = append(pool, scored{mem: models.EpisodicMemory{
			ID:         item.ID,
			ProjectID:  item.ProjectID,
			Language:   item.Language,
			TaskType:   item.TaskType,
			Summary:    item.Summary,
			Weight:     item.Weight,
			OccurredAt: item.OccurredAt,
		}, score: bigramOverlap(query, item.Summary)})
	}

	// Relevance gate: only overlapping items are candidates.
	var relevant []scored
	for _, s := range pool {
		if s.score > 0 {
			relevant = append(relevant, s)
		}
	}
	sort.Slice(relevant, func(i, j int) bool { return relevant[i].score > relevant[j].score })
	if window.SortNewest {
		// Rank-then-sort: among the top-limit relevant memories, order by
		// occurred_at descending so "最近一次" surfaces the most recent of the
		// relevant set.
		top := limit
		if top > len(relevant) {
			top = len(relevant)
		}
		topRelevant := relevant[:top]
		sort.Slice(topRelevant, func(i, j int) bool {
			return topRelevant[i].mem.EffectiveOccurredAt().After(topRelevant[j].mem.EffectiveOccurredAt())
		})
	}

	out := make([]models.EpisodicMemory, 0, limit)
	for _, s := range relevant {
		out = append(out, s.mem)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// RetrievePitfalls finds pitfalls relevant to query within a project. It
// pulls a weight-ranked candidate pool from the store and scores them by
// text overlap; entity references in the query get a boost so
// entity-anchored pitfalls surface. Only injectable pitfalls are returned
// (confirmed or high-confidence proposed — the v0.2.2 Workbench state
// machine); suppressed/obsolete pitfalls are the injection escape valve.
func RetrievePitfalls(ctx context.Context, deps core.WriteDeps, projectID, query string, limit int) ([]models.PitfallMemory, error) {
	if limit <= 0 {
		limit = pitfallResultLimit
	}

	candidates, err := deps.Store.SearchPitfall(ctx, store.PitfallSearchRequest{
		ProjectID: projectID,
		MinWeight: 0.5,
		Limit:     pitfallCandidatePool,
	})
	if err != nil {
		return nil, err
	}

	entityIDs := entityRefRe.FindAllString(query, -1)
	queryBigrams := charBigrams(query)

	type scored struct {
		p     models.PitfallMemory
		score float64
	}
	var pool []scored
	for _, p := range candidates {
		if !p.Injectable() {
			continue
		}
		s := bigramOverlapFromSets(queryBigrams, charBigrams(p.Signature))
		for _, e := range entityIDs {
			if strings.Contains(strings.ToLower(p.EntityID), strings.ToLower(strings.TrimPrefix(e, "@"))) {
				s += 0.3
			}
		}
		if s > 0 {
			pool = append(pool, scored{p: p, score: s})
		}
	}

	sort.Slice(pool, func(i, j int) bool { return pool[i].score > pool[j].score })

	out := make([]models.PitfallMemory, 0, limit)
	for _, s := range pool {
		out = append(out, s.p)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// FormatPrefetch renders episodes + pitfalls into the plain-text recall block
// hermes wraps in <memory-context>. Pitfalls use the §6 risk-advisory shape.
func FormatPrefetch(episodes []models.EpisodicMemory, pitfalls []models.PitfallMemory) string {
	var b strings.Builder

	if len(episodes) > 0 {
		b.WriteString("[vatbrain memory context]\n")
		for i, ep := range episodes {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "- %s", oneLine(ep.Summary))
			if ep.Language != "" {
				fmt.Fprintf(&b, " (lang: %s)", ep.Language)
			}
		}
	}

	for _, p := range pitfalls {
		b.WriteString("\n\n[Risk advisory — vatbrain]\n")
		fmt.Fprintf(&b, "- %s: %s\n", p.EntityID, oneLine(p.Signature))
		when := "—"
		if p.LastOccurredAt != nil {
			when = p.LastOccurredAt.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "  root cause: %s · 曾于 %s 发生 · confidence: %d\n",
			p.RootCauseCategory, when, int(p.TrustLevel))
		if p.FixStrategy != "" {
			fmt.Fprintf(&b, "  Fix: %s\n", oneLine(p.FixStrategy))
		}
	}

	return strings.TrimSpace(b.String())
}

// oneLine collapses newlines so recall text stays parseable by hermes.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// hasVectorMagnitude reports whether v has any non-zero component.
func hasVectorMagnitude(v []float32) bool {
	for _, c := range v {
		if c != 0 {
			return true
		}
	}
	return false
}

// bigramOverlap returns the Dice coefficient of the character-bigram sets of
// a and b. Character bigrams work for both Chinese and Latin text.
func bigramOverlap(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	return bigramOverlapFromSets(charBigrams(a), charBigrams(b))
}

// bigramOverlapFromSets computes the Dice coefficient of two bigram sets.
func bigramOverlapFromSets(ab, bb map[string]struct{}) float64 {
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

// charBigrams builds the character-bigram set of s.
func charBigrams(s string) map[string]struct{} {
	r := []rune(s)
	if len(r) == 1 {
		return map[string]struct{}{string(r[0]): {}}
	}
	out := make(map[string]struct{}, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		out[string(r[i:i+2])] = struct{}{}
	}
	return out
}
