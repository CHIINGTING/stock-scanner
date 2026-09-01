package candidate

import (
	"fmt"
	"io"
	"strings"
)

// Text rendering for the CLI.
//
// It takes a writer rather than printing, so a test can read what a human would see instead
// of capturing global stdout — the output IS the contract here, and a contract nothing checks
// drifts.

// RenderText writes the human-readable research summary.
func RenderText(w io.Writer, views []View) {
	if len(views) == 0 {
		fmt.Fprintln(w, "No candidates configured.")
		return
	}
	for i, v := range views {
		if i > 0 {
			fmt.Fprintln(w)
		}
		renderOne(w, v)
	}
	fmt.Fprintf(w, "\n%d candidates.\n", len(views))
}

func renderOne(w io.Writer, v View) {
	head := v.Symbol
	if v.Name != "" {
		head += " " + v.Name
	}
	fmt.Fprintln(w, head)
	if v.Note != "" {
		fmt.Fprintf(w, "  Note:             %s\n", v.Note)
	}

	if !v.HasScanner {
		// No entry: say why, and do NOT print a price, an action or a score. A "0.00" here
		// would be read as a real price by anyone skimming.
		fmt.Fprintf(w, "  Status:           %s\n", v.Status)
		if v.Reason != "" {
			fmt.Fprintf(w, "  Reason:           %s\n", v.Reason)
		}
		fmt.Fprintf(w, "  Data Quality:     %s\n", v.DataQuality)
		return
	}

	fmt.Fprintf(w, "  Price:            %s\n", v.Price)
	// Labelled as the scanner's, every time. These are existing evidence, not a verdict this
	// command reached — and a reader who confuses the two would think the research layer is
	// recommending something.
	fmt.Fprintf(w, "  Action:           %s   (scanner)\n", v.Action)
	fmt.Fprintf(w, "  Stage:            %s   (scanner)\n", v.Stage)
	fmt.Fprintf(w, "  RocketScore:      %d   (scanner)\n", v.RocketScore)
	if v.ExplosionProb != "" {
		fmt.Fprintf(w, "  ExplosionProb:    %s   (scanner)\n", v.ExplosionProb)
	}
	fmt.Fprintln(w)

	for _, b := range v.Blocks {
		line := fmt.Sprintf("  %-17s %s", b.Name+":", b.Status)
		if b.Detail != "" {
			line += "   " + b.Detail
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
	renderFundamental(w, v.Fundamental)
	renderValuation(w, v.Valuation)
	fmt.Fprintf(w, "  %-17s %s\n", "Data Quality:", v.DataQuality)
}

// renderFundamental prints the ACTUAL reported financials.
//
// Every line is already a string from the view — this function does no arithmetic, which is
// what stops the CLI and any future HTML page from computing a margin two different ways.
func renderFundamental(w io.Writer, f FundamentalView) {
	fmt.Fprintf(w, "\n  Fundamentals (Actual)\n")
	fmt.Fprintf(w, "    %-15s %s\n", "Status:", f.Status)
	if f.Status != Available && f.Status != Partial {
		if f.Reason != "" {
			fmt.Fprintf(w, "    %-15s %s\n", "Reason:", f.Reason)
		}
		return
	}
	if f.RevenueMonth != "" {
		fmt.Fprintf(w, "    %-15s %s\n", "Revenue Month:", f.RevenueMonth)
	}
	fmt.Fprintf(w, "    %-15s %s\n", "Revenue:", f.Revenue)
	fmt.Fprintf(w, "    %-15s %s\n", "Revenue YoY:", f.RevenueYoY)
	fmt.Fprintf(w, "    %-15s %s\n", "Revenue MoM:", f.RevenueMoM)
	if f.Period != "" {
		// The period label carries the cumulative marker, because this source reports
		// year-to-date and a reader who takes it for one quarter will be wrong by half.
		fmt.Fprintf(w, "    %-15s %s\n", "Period:", f.Period)
		fmt.Fprintf(w, "    %-15s %s\n", "累計 EPS:", f.CumulativeEPS)
	}
	fmt.Fprintf(w, "    %-15s %s\n", "Gross Margin:", f.GrossMargin)
	fmt.Fprintf(w, "    %-15s %s\n", "Op Margin:", f.OperatingMargin)
	if f.PublishedAt != "" {
		fmt.Fprintf(w, "    %-15s %s\n", "Published:", f.PublishedAt)
	}
	if f.Source != "" {
		fmt.Fprintf(w, "    %-15s %s\n", "Source:", f.Source)
	}
}

// renderValuation prints the price-relative measures.
//
// The forward-looking lines print their STATUS rather than a dash. "NOT_IMPLEMENTED" tells a
// reader there is no source for the number; a dash would leave them wondering whether the
// computation failed, or whether they were looking at the wrong stock.
func renderValuation(w io.Writer, v ValuationView) {
	fmt.Fprintf(w, "\n  Valuation\n")
	fmt.Fprintf(w, "    %-19s %s\n", "Status:", v.Status)
	if v.Status == Disabled {
		return
	}
	if v.TrailingDate != "" {
		fmt.Fprintf(w, "    %-19s %s   (%s)\n", "Trailing P/E:", v.TrailingPE, v.TrailingDate)
	} else {
		fmt.Fprintf(w, "    %-19s %s\n", "Trailing P/E:", v.TrailingPE)
	}
	fmt.Fprintf(w, "    %-19s %s\n", "P/B:", v.PBRatio)
	fmt.Fprintf(w, "    %-19s %s\n", "Dividend Yield:", v.DividendYield)

	fmt.Fprintf(w, "    %-19s %s  (%d samples", "Historical P/E:", v.HistoryStatus, v.SampleCount)
	if v.WindowDays > 0 {
		fmt.Fprintf(w, ", %dd window", v.WindowDays)
	}
	fmt.Fprintln(w, ")")
	if v.HistoryStatus == Available {
		fmt.Fprintf(w, "      %-17s %s\n", "Median:", v.MedianPE)
		fmt.Fprintf(w, "      %-17s %s / %s\n", "P25 / P75:", v.P25PE, v.P75PE)
		fmt.Fprintf(w, "      %-17s %s\n", "Current percentile:", v.Percentile)
	}

	fmt.Fprintf(w, "    %-19s %s\n", "Forward P/E:", v.ForwardPE)
	fmt.Fprintf(w, "    %-19s %s\n", "PEG:", v.PEG)
	fmt.Fprintf(w, "    %-19s %s\n", "Fair Value:", v.FairValue)
	if v.Source != "" {
		fmt.Fprintf(w, "    %-19s %s\n", "Source:", v.Source)
	}
}
