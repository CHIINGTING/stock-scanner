package news

import (
	"sort"
	"strings"
	"time"
)

// EpisodeLine is one classified point within an episode: a direction + the entities it
// named + the evidence sentence. Multiple lines per episode (one per signal group).
type EpisodeLine struct {
	Signal   SignalType `json:"signal"`
	Entities string     `json:"entities"` // "功率、記憶體、國巨(2327)"
	Evidence string     `json:"evidence,omitempty"`
	Strength int        `json:"strength"`
	Conflict bool       `json:"conflict,omitempty"`
}

// EpisodeView is one 股癌 episode's aggregated intelligence (episode axis), for the
// per-episode cards page. Built from the NewsSignals of a single EventID.
type EpisodeView struct {
	EP          int              `json:"ep"`    // 677 (0 for non-episodic)
	Label       string           `json:"label"` // "EP677" / "事件"
	PublishedAt time.Time        `json:"published_at"`
	RawTitle    string           `json:"raw_title,omitempty"`
	Sources     []NewsSource     `json:"sources,omitempty"`
	Lines       []EpisodeLine    `json:"lines,omitempty"`
	Stocks      []StockReference `json:"stocks,omitempty"`
	Sectors     []string         `json:"sectors,omitempty"`
	Excerpt     string           `json:"excerpt,omitempty"`
	Conflict    bool             `json:"conflict,omitempty"` // any cross-source conflict in this episode
}

// BuildEpisodes groups signals by episode (EventID) into per-episode cards, newest first.
// It is display-only, derived from the same signals persisted in the snapshot.
func BuildEpisodes(signals []NewsSignal) []EpisodeView {
	type acc struct {
		ev        EpisodeView
		srcSeen   map[string]bool
		stockSeen map[string]bool
		secSeen   map[string]bool
	}
	byEP := map[string]*acc{}
	var order []string

	for _, sig := range signals {
		a, ok := byEP[sig.EventID]
		if !ok {
			label := epLabel(sig.EventID)
			if label == "" {
				label = "事件"
			}
			a = &acc{
				ev:        EpisodeView{EP: EpisodeNumber(sig.EventID), Label: label, PublishedAt: sig.PublishedAt},
				srcSeen:   map[string]bool{},
				stockSeen: map[string]bool{},
				secSeen:   map[string]bool{},
			}
			byEP[sig.EventID] = a
			order = append(order, sig.EventID)
		}
		if a.ev.RawTitle == "" {
			a.ev.RawTitle = sig.RawTitle
		}
		if len(sig.ContentExcerpt) > len(a.ev.Excerpt) {
			a.ev.Excerpt = sig.ContentExcerpt
		}
		if sig.Conflict {
			a.ev.Conflict = true
		}
		for _, s := range sig.Sources {
			if k := s.Provider + "|" + s.URL; !a.srcSeen[k] {
				a.srcSeen[k] = true
				a.ev.Sources = append(a.ev.Sources, s)
			}
		}
		for _, st := range sig.Stocks {
			if st.Code != "" && !a.stockSeen[st.Code] {
				a.stockSeen[st.Code] = true
				a.ev.Stocks = append(a.ev.Stocks, st)
			}
		}
		for _, sec := range sig.Sectors {
			if !a.secSeen[sec] {
				a.secSeen[sec] = true
				a.ev.Sectors = append(a.ev.Sectors, sec)
			}
		}
		a.ev.Lines = append(a.ev.Lines, EpisodeLine{
			Signal: sig.Signal, Entities: episodeEntities(sig),
			Evidence: sig.Summary, Strength: sig.Strength, Conflict: sig.Conflict,
		})
	}

	out := make([]EpisodeView, 0, len(order))
	for _, k := range order {
		a := byEP[k]
		sort.SliceStable(a.ev.Lines, func(i, j int) bool { return a.ev.Lines[i].Strength > a.ev.Lines[j].Strength })
		sort.Strings(a.ev.Sectors)
		sort.Slice(a.ev.Stocks, func(i, j int) bool { return a.ev.Stocks[i].Code < a.ev.Stocks[j].Code })
		sort.SliceStable(a.ev.Sources, func(i, j int) bool { return a.ev.Sources[i].Provider < a.ev.Sources[j].Provider })
		out = append(out, a.ev)
	}
	// newest first: EP desc, then non-episodic by publish time desc.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].EP != out[j].EP {
			return out[i].EP > out[j].EP
		}
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	return out
}

func episodeEntities(sig NewsSignal) string {
	parts := append([]string(nil), sig.Sectors...)
	for _, st := range sig.Stocks {
		switch {
		case st.Code != "":
			parts = append(parts, st.Name+"("+st.Code+")")
		case st.Name != "":
			parts = append(parts, st.Name)
		}
	}
	return strings.Join(parts, "、")
}
