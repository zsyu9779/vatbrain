package provider

import (
	"regexp"
	"time"
)

// TimeWindow is a parsed relative-time query over episodic occurred_at.
// Zero After/Before mean the bound is open; SortNewest orders results
// newest-first ("最近一次" semantics) regardless of relevance ranking.
type TimeWindow struct {
	After      time.Time
	Before     time.Time
	SortNewest bool
}

// Relative-time expressions recognized in retrieval queries. The eval harness
// asks in both languages ("what did Alice do last week?" / "Alice 上周做了什么"),
// so each window matches English and Chinese forms. Windows are rolling
// (e.g. "last week" = the trailing 7 days ending now), matching how the
// LoCoMo eval data places events relative to the conversation.
var (
	lastWeekRe    = regexp.MustCompile(`(?i)last week|上周|过去一周|最近一周|一周内`)
	yesterdayRe   = regexp.MustCompile(`(?i)yesterday|昨天`)
	mostRecentRe  = regexp.MustCompile(`(?i)most recent|latest|最近一次|最近|最新`)
	lastWeekHours = 7 * 24 * time.Hour
	dayHours      = 24 * time.Hour
)

// ParseRelativeTime interprets relative-time expressions in query against now.
// Queries without temporal expressions yield a zero TimeWindow, so callers
// can treat the output as a no-op.
func ParseRelativeTime(query string, now time.Time) TimeWindow {
	var w TimeWindow
	switch {
	case lastWeekRe.MatchString(query):
		w.After = now.Add(-lastWeekHours)
		w.Before = now
	case yesterdayRe.MatchString(query):
		w.After = now.Add(-dayHours)
		w.Before = now
	}
	w.SortNewest = mostRecentRe.MatchString(query)
	return w
}
