package news

import (
	"sort"
	"strings"
	"time"
)

// Classification is a transparent keyword heuristic — deliberately NOT an LLM (Phase-1
// constraint). It is shadow/display context only. It handles negation and a few
// compound phrases so obvious inversions ("突破失敗", "沒有轉弱") are not misread, and
// softens a bearish read that is hedged by a support cue ("修正…但仍在季線之上") into a
// caution (RISK_WARNING) rather than a strong BEARISH.

type cueSet struct {
	typ    SignalType
	strong []string // +2 each
	weak   []string // +1 each
}

// lexicon order also breaks ties (earlier category wins on equal score).
var lexicon = []cueSet{
	{SignalRotationIn, []string{"資金輪動進", "輪動進場", "資金進駐", "資金流入", "卡位"}, []string{"換股進", "布局"}},
	{SignalRotationOut, []string{"資金輪動出", "輪動出場", "資金流出", "資金撤"}, []string{"退場", "換股出", "調節"}},
	{SignalBullish, []string{"看好", "強勢", "領漲", "噴出", "續強", "上攻", "突破"}, []string{"轉強", "買點", "加碼", "偏多"}},
	{SignalBearish, []string{"看空", "走弱", "賣壓", "補跌", "出貨", "破底", "下殺", "跌破", "失守", "季線之下"}, []string{"轉弱", "減碼", "偏空"}},
	{SignalRiskWarning, []string{"利空", "警示", "風險升高"}, []string{"風險", "注意", "小心", "修正", "拉回", "休息", "整理", "獲利了結", "震盪"}},
}

// failurePhrases invert an embedded bullish token: "突破失敗" must never read BULLISH.
// They add a bearish cue and are stripped before token scoring so the inner 突破/反彈
// no longer counts as positive.
var failurePhrases = []string{"突破失敗", "反彈失敗", "假突破", "突破未果", "攻高失敗", "過高失敗"}

// supportCues hedge a bearish read into caution ("修正…但仍在季線之上" → RISK_WARNING).
var supportCues = []string{"仍在季線之上", "站回", "守住", "撐住", "支撐", "未跌破", "止穩", "仍守", "仍強", "撐盤", "季線之上"}

// negators cancel a cue when they appear just before it ("沒有轉弱", "尚未突破").
var negators = []string{"沒有", "尚未", "沒", "無", "未"}

// TODO(news): proximity-based sentiment attribution for long compound sentences — a cue
// (e.g. 強勢) can attach to an entity elsewhere in the same long sentence ("有很多強勢族群…
// 被動元件也進入修正"). Weight cues by distance to the entity, or attribute per clause,
// instead of one direction per whole sentence. Conflict=true currently keeps the mixed
// read visible; this refinement is deferred (would be scope creep).

// classifySentence returns the dominant direction of one sentence and a 0..n cue score.
func classifySentence(sent string) (SignalType, int, bool) {
	work := sent
	bearishBonus := 0
	for _, fp := range failurePhrases {
		for strings.Contains(work, fp) {
			bearishBonus += 2
			work = strings.Replace(work, fp, "", 1)
		}
	}

	scores := map[SignalType]int{}
	for _, cs := range lexicon {
		for _, w := range cs.strong {
			scores[cs.typ] += 2 * countCue(work, w)
		}
		for _, w := range cs.weak {
			scores[cs.typ] += 1 * countCue(work, w)
		}
	}
	scores[SignalBearish] += bearishBonus

	best := SignalNeutral
	bestScore := 0
	for _, cs := range lexicon { // lexicon order = tie-break priority
		if scores[cs.typ] > bestScore {
			bestScore, best = scores[cs.typ], cs.typ
		}
	}
	if bestScore == 0 {
		return SignalNeutral, 0, false
	}

	// A bearish read tempered by a support/hedge cue is caution, not strong bearish.
	if best == SignalBearish && hasAny(work, supportCues) {
		return SignalRiskWarning, maxInt(1, bestScore-1), true
	}
	return best, bestScore, true
}

// countCue counts occurrences of cue not immediately preceded by a negator (within a
// short window), so "沒有轉弱"/"尚未突破" do not fire the cue.
func countCue(s, cue string) int {
	n := 0
	for start := 0; start <= len(s); {
		i := strings.Index(s[start:], cue)
		if i < 0 {
			break
		}
		pos := start + i
		if !negatedAt(s, pos) {
			n++
		}
		start = pos + len(cue)
	}
	return n
}

func negatedAt(s string, pos int) bool {
	lo := pos - 12 // ~4 CJK runes
	if lo < 0 {
		lo = 0
	}
	win := s[lo:pos]
	for _, neg := range negators {
		if strings.Contains(win, neg) {
			return true
		}
	}
	return false
}

func hasAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// splitSentences splits on sentence terminators ONLY — NOT the in-sentence comma "，" —
// so a hedged clause ("…修正，但仍在季線之上") stays one unit and support-softening applies.
var sentenceSeps = func() *strings.Replacer {
	seps := []string{"。", "！", "？", "!", "?", "\n", "；", ";"}
	pairs := make([]string, 0, len(seps)*2)
	for _, s := range seps {
		pairs = append(pairs, s, "\x1f")
	}
	return strings.NewReplacer(pairs...)
}()

func splitSentences(text string) []string {
	raw := strings.Split(sentenceSeps.Replace(text), "\x1f")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// BuildSignals converts one event into per-entity directional signals. EVERY source's
// content is classified (not just the longest), so a second source's differing opinion
// is preserved rather than dropped. Signals sharing the same (entity-set, direction)
// merge (evidence appended, strength summed, contributing providers unioned). When the
// same entity ends up with opposing-polarity signals in the event, all of them are
// flagged Conflict=true so the conflict stays visible.
func BuildSignals(ev Event, idx *ResolveIndex, now time.Time) []NewsSignal {
	confidence := eventConfidence(ev)
	rawTitle := ev.Title()

	type agg struct {
		sig       NewsSignal
		strength  int
		providers map[string]bool
	}
	byKey := map[string]*agg{}
	var order []string
	seenItem := map[string]bool{}

	for _, item := range ev.Items {
		content := item.Content
		if content == "" {
			content = item.Title
		}
		dupKey := item.Source + "\x1f" + content
		if content == "" || seenItem[dupKey] {
			continue // same-source repeated fetch → never double-count
		}
		seenItem[dupKey] = true
		excerpt := makeExcerpt(content)

		for _, sent := range splitSentences(content) {
			typ, score, ok := classifySentence(sent)
			if !ok {
				continue
			}
			stocks := idx.ResolveStocks(sent)
			sectors := idx.ResolveSectors(sent)
			if len(stocks) == 0 && len(sectors) == 0 {
				continue
			}
			key := signalKey(stocks, sectors, typ)
			a, ok := byKey[key]
			if !ok {
				a = &agg{
					sig: NewsSignal{
						EventID:        ev.EventID,
						PublishedAt:    ev.PublishedAt,
						ObservedAt:     now,
						Stocks:         stocks,
						Sectors:        sectors,
						Signal:         typ,
						Confidence:     confidence,
						RawTitle:       rawTitle,
						ContentExcerpt: excerpt,
					},
					providers: map[string]bool{},
				}
				byKey[key] = a
				order = append(order, key)
			}
			a.strength += score
			a.providers[item.Source] = true
			ev := trimEvidence(sent)
			if len(a.sig.Evidence) < 4 && !containsStr(a.sig.Evidence, ev) {
				a.sig.Evidence = append(a.sig.Evidence, ev)
				if a.sig.Summary == "" {
					a.sig.Summary = ev
				}
			}
		}
	}

	signals := make([]NewsSignal, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		a.sig.Strength = clampStrength(a.strength)
		a.sig.Sources = providersToSources(a.providers, ev)
		signals = append(signals, a.sig)
	}
	markConflicts(signals)
	sort.SliceStable(signals, func(i, j int) bool { return signals[i].Strength > signals[j].Strength })
	return signals
}

func signalKey(stocks []StockReference, sectors []string, typ SignalType) string {
	var b strings.Builder
	for _, s := range stocks {
		b.WriteString(s.Code)
		b.WriteByte(',')
	}
	b.WriteByte('|')
	for _, s := range sectors {
		b.WriteString(s)
		b.WriteByte(',')
	}
	b.WriteByte('|')
	b.WriteString(string(typ))
	return b.String()
}

// providersToSources maps the contributing provider set to sources (provider+URL),
// resolving each provider's URL from the event's items. Deterministic order.
func providersToSources(providers map[string]bool, ev Event) []NewsSource {
	urlOf := map[string]string{}
	for _, it := range ev.Items {
		if _, ok := urlOf[it.Source]; !ok {
			urlOf[it.Source] = it.URL
		}
	}
	out := make([]NewsSource, 0, len(providers))
	for p := range providers {
		out = append(out, NewsSource{Provider: p, URL: urlOf[p]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// markConflicts flags every signal whose entity (stock or sector) has both a bullish-ish
// and a bearish-ish signal within the same event (e.g. SWD ROTATION_OUT vs TWETQ BULLISH
// on 被動元件). polarity() is used ONLY here and in divergence detection.
func markConflicts(signals []NewsSignal) {
	pol := map[string]map[int]bool{}
	entitiesOf := func(s NewsSignal) []string {
		var es []string
		for _, st := range s.Stocks {
			if st.Code != "" {
				es = append(es, "s:"+st.Code)
			}
		}
		for _, sec := range s.Sectors {
			es = append(es, "k:"+sec)
		}
		return es
	}
	for _, s := range signals {
		p := polarity(s.Signal)
		if p == 0 {
			continue
		}
		for _, e := range entitiesOf(s) {
			if pol[e] == nil {
				pol[e] = map[int]bool{}
			}
			pol[e][p] = true
		}
	}
	for i := range signals {
		for _, e := range entitiesOf(signals[i]) {
			if pol[e][1] && pol[e][-1] {
				signals[i].Conflict = true
				break
			}
		}
	}
}

func eventConfidence(ev Event) int {
	c := 3
	if EpisodeNumber(ev.EventID) > 0 {
		c += 3 // stable episode id
	}
	c += len(ev.Sources) // corroborating sources
	return clampStrength(c)
}

func clampStrength(n int) int {
	if n < 0 {
		return 0
	}
	if n > 10 {
		return 10
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func trimEvidence(s string) string {
	r := []rune(strings.TrimSpace(s))
	const max = 140
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return string(r)
}

// makeExcerpt returns a whitespace-collapsed ~240-rune excerpt for historical audit.
func makeExcerpt(content string) string {
	r := []rune(strings.Join(strings.Fields(content), " "))
	const max = 240
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return string(r)
}
