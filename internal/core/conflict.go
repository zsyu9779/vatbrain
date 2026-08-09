package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vatbrain/vatbrain/internal/models"
	"github.com/vatbrain/vatbrain/internal/store"
)

// ErrConflictStoreUnsupported is returned when the backing store does not
// implement store.RuleConflictStore (conflict governance is optional).
var ErrConflictStoreUnsupported = errors.New("conflict governance: backing store does not support rule conflicts")

// Polarity classifies the directive a rule carries. Two rules about the same
// subject with opposite polarity are a candidate conflict.
type Polarity string

const (
	// PolarityNeutral means the rule carries no directive (informational).
	PolarityNeutral Polarity = "neutral"
	// PolarityProhibitive forbids an approach ("不要用文本 gsub").
	PolarityProhibitive Polarity = "prohibitive"
	// PolarityAffirmative prescribes an approach ("应该用 yaml 库").
	PolarityAffirmative Polarity = "affirmative"
)

// prohibitiveMarkers forbid an approach. Prohibitive wins over affirmative so
// "不要使用 X" is prohibitive, not affirmative.
var prohibitiveMarkers = []string{
	"不要", "禁止", "别用", "不要用", "别再用", "避免", "不应", "不能", "不可", "不该",
	"never", "don't", "do not", "must not", "mustn't", "avoid", "forbid", "禁止使用",
}

// affirmativeMarkers prescribe an approach.
var affirmativeMarkers = []string{
	"应该", "应当", "必须", "一定要", "始终", "总是", "always", "must", "should",
	"使用", "采用", "选用", "需要",
}

// DetectPolarity returns the directive polarity of a rule's content and whether
// it is a directive at all. Lowercasing Latin before matching keeps "Don't"
// and "don't" equivalent.
func DetectPolarity(content string) (Polarity, bool) {
	norm := strings.ToLower(content)
	for _, m := range prohibitiveMarkers {
		if strings.Contains(norm, strings.ToLower(m)) {
			return PolarityProhibitive, true
		}
	}
	for _, m := range affirmativeMarkers {
		if strings.Contains(norm, strings.ToLower(m)) {
			return PolarityAffirmative, true
		}
	}
	return PolarityNeutral, false
}

// ConflictPair is a detected contradiction between two active semantic rules.
type ConflictPair struct {
	RuleA  models.SemanticMemory
	RuleB  models.SemanticMemory
	Reason string
}

// ConflictDetector finds candidate conflicts among a set of semantic rules. The
// v0.3 heuristic is deterministic and CJK-safe: two active rules of the same
// memory type that share enough character-bigram content ("about the same
// thing") while carrying opposite directives are a conflict.
type ConflictDetector struct {
	// MinBigramSimilarity is the Dice coefficient on character bigrams above
	// which two rules count as being about the same subject. Default 0.25.
	MinBigramSimilarity float64
	// MaxRulesPerType caps the rule bucket size per memory type so the O(n²)
	// pairing stays bounded on large rule sets. Default 50.
	MaxRulesPerType int
}

// DefaultConflictDetector returns a ConflictDetector with tuned defaults.
func DefaultConflictDetector() *ConflictDetector {
	return &ConflictDetector{
		MinBigramSimilarity: 0.25,
		MaxRulesPerType:     50,
	}
}

// Detect returns the conflicting pairs among rules. It is pure (no I/O) so the
// heuristic is unit-testable in isolation.
func (d *ConflictDetector) Detect(rules []models.SemanticMemory) []ConflictPair {
	minSim := d.MinBigramSimilarity
	if minSim <= 0 {
		minSim = 0.25
	}
	maxPerType := d.MaxRulesPerType
	if maxPerType <= 0 {
		maxPerType = 50
	}

	// Bucket active rules by type so we never pair a RULE with a CONSTRAINT.
	buckets := map[models.MemoryType][]models.SemanticMemory{}
	for _, r := range rules {
		if r.ObsoletedAt != nil {
			continue
		}
		if len(buckets[r.Type]) >= maxPerType {
			continue
		}
		buckets[r.Type] = append(buckets[r.Type], r)
	}

	var pairs []ConflictPair
	for _, bucket := range buckets {
		for i := 0; i < len(bucket); i++ {
			for j := i + 1; j < len(bucket); j++ {
				a, b := bucket[i], bucket[j]
				pa, aOK := DetectPolarity(a.Content)
				pb, bOK := DetectPolarity(b.Content)
				if !aOK || !bOK || pa == pb {
					continue
				}
				// Same subject: character-bigram overlap above threshold.
				if bigramDice(a.Content, b.Content) < minSim {
					continue
				}
				pairs = append(pairs, ConflictPair{
					RuleA:  a,
					RuleB:  b,
					Reason: fmt.Sprintf("%q (%s, trust %d) vs %q (%s, trust %d)",
						oneLine(a.Content), pa, a.TrustLevel,
						oneLine(b.Content), pb, b.TrustLevel),
				})
			}
		}
	}
	return pairs
}

// ConflictResolver applies §11 source grading: the higher-trust rule wins and
// the loser is retired (obsoleted); equal trust defers to a human.
type ConflictResolver struct{}

// ResolveOutcome reports what Resolve decided.
type ResolveOutcome struct {
	Status   models.ConflictStatus
	WinnerID uuid.UUID
	LoserID  uuid.UUID
}

// Resolve settles a detected conflict. On auto-resolution it marks the losing
// rule obsolete, records a CONFLICTS_WITH edge from winner to loser, and
// updates the conflict record. On equal trust it leaves the record pending.
func (r *ConflictResolver) Resolve(
	ctx context.Context,
	s store.MemoryStore,
	conflict *models.RuleConflict,
	ruleA, ruleB models.SemanticMemory,
) (ResolveOutcome, error) {
	cs, ok := s.(store.RuleConflictStore)
	if !ok {
		return ResolveOutcome{}, ErrConflictStoreUnsupported
	}

	if ruleA.TrustLevel == ruleB.TrustLevel {
		return ResolveOutcome{Status: models.ConflictPending}, nil
	}

	now := time.Now().UTC()
	winner, loser := ruleA, ruleB
	if ruleB.TrustLevel > ruleA.TrustLevel {
		winner, loser = ruleB, ruleA
	}

	if err := cs.MarkSemanticObsolete(ctx, loser.ID, now); err != nil {
		return ResolveOutcome{}, fmt.Errorf("retire losing rule %s: %w", loser.ID, err)
	}
	_ = s.CreateEdge(ctx, winner.ID, loser.ID, "CONFLICTS_WITH",
		map[string]any{"resolution": "winner"})
	reason := fmt.Sprintf("trust %d > %d — lower-trust rule retired",
		winner.TrustLevel, loser.TrustLevel)
	if err := cs.ResolveRuleConflict(ctx, conflict.ID, models.ConflictAutoResolved,
		winner.ID, reason, now); err != nil {
		return ResolveOutcome{}, fmt.Errorf("record auto resolution: %w", err)
	}

	return ResolveOutcome{
		Status:   models.ConflictAutoResolved,
		WinnerID: winner.ID,
		LoserID:  loser.ID,
	}, nil
}

// ResolveManually settles a pending conflict from a human decision. The given
// winner must be one of the two rules in the record; the other is retired.
func ResolveManually(
	ctx context.Context,
	s store.MemoryStore,
	conflict *models.RuleConflict,
	winnerRuleID uuid.UUID,
	note string,
) error {
	cs, ok := s.(store.RuleConflictStore)
	if !ok {
		return ErrConflictStoreUnsupported
	}

	var loserID uuid.UUID
	switch winnerRuleID {
	case conflict.RuleAID:
		loserID = conflict.RuleBID
	case conflict.RuleBID:
		loserID = conflict.RuleAID
	default:
		return fmt.Errorf("winner %s is not one of the conflicted rules %s/%s",
			winnerRuleID, conflict.RuleAID, conflict.RuleBID)
	}

	now := time.Now().UTC()
	if err := cs.MarkSemanticObsolete(ctx, loserID, now); err != nil {
		return fmt.Errorf("retire losing rule %s: %w", loserID, err)
	}
	_ = s.CreateEdge(ctx, winnerRuleID, loserID, "CONFLICTS_WITH",
		map[string]any{"resolution": "manual"})
	reason := note
	if strings.TrimSpace(reason) == "" {
		reason = "manual adjudication"
	}
	return cs.ResolveRuleConflict(ctx, conflict.ID, models.ConflictManualResolved,
		winnerRuleID, reason, now)
}

// DetectionResult summarises a detection pass.
type DetectionResult struct {
	Detected     int `json:"detected"`
	AutoResolved int `json:"auto_resolved"`
	Pending      int `json:"pending"`
}

// RunRuleConflictDetection scans a project's active semantic rules, saves newly
// detected conflicts, and auto-resolves any with unequal trust. Equal-trust
// conflicts are left pending for the human Workbench.
func RunRuleConflictDetection(ctx context.Context, s store.MemoryStore, projectID string) (DetectionResult, error) {
	cs, ok := s.(store.RuleConflictStore)
	if !ok {
		return DetectionResult{}, ErrConflictStoreUnsupported
	}

	rules, err := s.SearchSemantic(ctx, store.SemanticSearchRequest{
		ProjectID: projectID,
		Limit:     1000,
	})
	if err != nil {
		return DetectionResult{}, fmt.Errorf("conflict detection: fetch rules: %w", err)
	}

	// Skip pairs already tracked so repeated runs are idempotent.
	existing, err := cs.ListRuleConflicts(ctx, "", 2000)
	if err != nil {
		return DetectionResult{}, fmt.Errorf("conflict detection: list existing: %w", err)
	}
	tracked := make(map[[2]uuid.UUID]struct{}, len(existing))
	for _, c := range existing {
		if c.Status == models.ConflictPending {
			tracked[pairKey(c.RuleAID, c.RuleBID)] = struct{}{}
		}
	}

	detector := DefaultConflictDetector()
	resolver := &ConflictResolver{}
	var result DetectionResult

	for _, pair := range detector.Detect(rules) {
		if _, seen := tracked[pairKey(pair.RuleA.ID, pair.RuleB.ID)]; seen {
			continue
		}
		conflict := &models.RuleConflict{
			ID:          uuid.New(),
			RuleAID:     pair.RuleA.ID,
			RuleBID:     pair.RuleB.ID,
			EntityGroup: pair.RuleA.EntityGroup,
			Basis:       models.ConflictBasisPolarity,
			Status:      models.ConflictPending,
			Reason:      pair.Reason,
			CreatedAt:   time.Now().UTC(),
		}
		if err := cs.SaveRuleConflict(ctx, conflict); err != nil {
			return result, fmt.Errorf("save conflict: %w", err)
		}
		result.Detected++

		outcome, err := resolver.Resolve(ctx, s, conflict, pair.RuleA, pair.RuleB)
		if err != nil {
			return result, err
		}
		if outcome.Status == models.ConflictAutoResolved {
			result.AutoResolved++
		} else {
			result.Pending++
		}
	}
	return result, nil
}

// pairKey canonicalises an unordered rule pair so A/B and B/A collide.
func pairKey(a, b uuid.UUID) [2]uuid.UUID {
	if a.String() < b.String() {
		return [2]uuid.UUID{a, b}
	}
	return [2]uuid.UUID{b, a}
}

// charBigramSet builds the set of character bigrams of s (CJK-safe).
func charBigramSet(s string) map[string]struct{} {
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

// bigramDice returns the Dice coefficient of two strings' character-bigram sets.
func bigramDice(a, b string) float64 {
	as, bs := charBigramSet(a), charBigramSet(b)
	if len(as) == 0 || len(bs) == 0 {
		return 0
	}
	inter := 0
	for g := range as {
		if _, ok := bs[g]; ok {
			inter++
		}
	}
	return 2.0 * float64(inter) / float64(len(as)+len(bs))
}

// oneLine collapses newlines so conflict reasons stay parseable.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
