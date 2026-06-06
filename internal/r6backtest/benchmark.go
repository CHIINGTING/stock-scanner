package r6backtest

// RunStopBenchmark applies every stop policy to the SAME entries per setup and
// returns one SetupStat per (setup, policy). Entries are collected ONCE per setup
// (stop-independent) so all policies are compared on identical entry points; the
// hold-to-horizon stats are therefore identical across a setup's policies.
func RunStopBenchmark(u *Universe, rs *RSPanel, setups []Setup, policies []StopPolicy, p Params) []SetupStat {
	maxH := maxHorizon(p)
	var stats []SetupStat
	for _, setup := range setups {
		eps := collectEntries(u, rs, setup, p)
		bucket := 0
		if len(eps) > 0 {
			bucket = eps[0].trig.Bucket
		}
		for _, pol := range policies {
			trades := make([]Trade, 0, len(eps))
			for _, ep := range eps {
				end := ep.entryIdx + maxH
				if end > len(ep.s.Candles)-1 {
					end = len(ep.s.Candles) - 1
				}
				sr := pol.Eval(ep.s, ep.entryIdx, ep.entry, p, end)
				if t := buildTrade(setup.Name(), rs, ep, sr, maxH); t != nil {
					trades = append(trades, *t)
				}
			}
			st := ComputeStats(setup.Name(), bucket, trades, p.Horizons, p)
			st.StopPolicy = pol.Name()
			stats = append(stats, st)
		}
	}
	return stats
}
