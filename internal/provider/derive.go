// Package provider implements the vatbrain-provider daemon: a stdio JSON-RPC
// server that hermes spawns as its memory provider backend. It reuses the
// shared core write pipeline so hermes turns flow through the same
// significance gate → pattern separation → persistence path as every other
// entry point.
package provider

import (
	"regexp"
	"strings"

	"github.com/vatbrain/vatbrain/internal/core"
)

// maxSummaryRunes bounds the derived summary length so one turn cannot flood
// the store with a giant transcript.
const maxSummaryRunes = 500

// Correction regex — 规则层（HERMES_INTEGRATION.md §5）。用户消息较短且命中
// 纠正动词即判定为纠错（prediction-error 信号）。
var (
	userConfirmedRe = regexp.MustCompile(`(?i)记住|记得|以后都|记一下|记到|remember this|remember that`)
	correctionRe    = regexp.MustCompile(
		`(?i)不对|错了|不是这样的|应该是|实际上应该是|actually|别用|不要用|别再用|改成|改回|纠正|` +
			`should be|don'?t use|not true|that'?s wrong|那个不对`,
	)
	// maxCorrectionRunes: §5 规定"用户消息短（<200 字符）+ 纠正动词"才规则命中。
	maxCorrectionRunes = 200
)

// DeriveWriteEvent builds a core.WriteEvent from a hermes turn. Phase 2
// implements the rule layer (§5): UserConfirmed and IsCorrection are detected
// by regex; CausedBehaviorChange needs adjacent tool-call diffs (Phase 4) and
// stays false here. An LLM classification fallback can be added behind the
// rule layer without changing this signature.
//
// An explicit memory instruction ("记住：不要用文本 gsub") is a confirmed
// write, not a correction, so UserConfirmed takes precedence over the
// correction rule.
func DeriveWriteEvent(userContent, assistantContent string) core.WriteEvent {
	event := core.WriteEvent{
		Summary:       deriveSummary(userContent),
		UserConfirmed: detectUserConfirmed(userContent),
	}
	if !event.UserConfirmed {
		event.IsCorrection = detectCorrection(userContent)
	}
	return event
}

// deriveSummary extracts the memory-worthy summary from a turn. It uses the
// user message, stripped of leading trivial filler and bounded in length.
func deriveSummary(userContent string) string {
	s := strings.TrimSpace(userContent)
	// Strip leading trivial filler so "继续：..." does not dominate.
	s = strings.TrimPrefix(s, "继续")
	s = trimLeadingPunct(s)

	r := []rune(s)
	if len(r) > maxSummaryRunes {
		return string(r[:maxSummaryRunes])
	}
	return s
}

// trimLeadingPunct trims whitespace and punctuation (both full-width and
// ASCII) from the start of s.
func trimLeadingPunct(s string) string {
	return strings.TrimLeft(s, " \t\n\r：:，,。.；;！!？?…— ")
}

// detectUserConfirmed reports whether the user message carries an explicit
// memory-storage instruction (§5: 记住/记得/以后都/remember this/记一下).
func detectUserConfirmed(text string) bool {
	return userConfirmedRe.MatchString(text)
}

// detectCorrection reports whether the user message is a correction of prior
// information (§5: short message + correction verb → prediction-error signal).
func detectCorrection(text string) bool {
	r := []rune(strings.TrimSpace(text))
	if len(r) >= maxCorrectionRunes {
		return false
	}
	return correctionRe.MatchString(text)
}
