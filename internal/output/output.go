package output

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sratabix/multidig/internal/dnsq"
	"github.com/sratabix/multidig/internal/geo"
	"github.com/sratabix/multidig/internal/report"
)

type Options struct {
	Query    dnsq.Options
	Expected []string
	Note     string
}

type Run struct {
	Results   map[string][]dnsq.Result
	Summaries map[string]report.Summary
	Elapsed   time.Duration
	Total     int
}

func Collect(ctx context.Context, opts Options) (Run, error) {
	start := time.Now()
	out := Run{
		Results:   map[string][]dnsq.Result{},
		Summaries: map[string]report.Summary{},
		Total:     len(opts.Query.Servers),
	}
	for res := range dnsq.Run(ctx, opts.Query) {
		out.Results[res.Type] = append(out.Results[res.Type], res)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Elapsed = time.Since(start)
	for _, t := range opts.Query.Types {
		out.Summaries[t] = report.Summarize(t, out.Total, out.Results[t], opts.Expected)
	}
	return out, nil
}

func (r Run) FullyPropagated() bool {
	for _, s := range r.Summaries {
		if s.Propagation() < 1 {
			return false
		}
	}
	return len(r.Summaries) > 0
}

type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

func Plain(ctx context.Context, w io.Writer, opts Options) (Run, error) {
	run, err := Collect(ctx, opts)
	if err != nil {
		return run, err
	}

	p := &printer{w: w}
	p.printf("%s · %d resolvers · %s\n", opts.Query.Domain, run.Total, fmtDuration(run.Elapsed))
	if opts.Note != "" {
		p.printf("%s\n", opts.Note)
	}

	for _, typ := range opts.Query.Types {
		s := run.Summaries[typ]
		results := run.Results[typ]

		p.printf("\n%s records — %s propagated\n", typ, pct(s.Propagation()))

		keyW := 34
		for _, g := range s.Groups {
			if n := utf8.RuneCountInString(g.Key); n > keyW {
				keyW = min(n, 60)
			}
		}
		for _, g := range s.Groups {
			marker := " "
			if g.Key == s.Consensus {
				marker = "*"
			}
			p.printf("  %s %-*s  %4s  %d/%d\n", marker, keyW, truncate(g.Key, keyW), pct(g.Share), g.Count, s.Done)
		}

		var cells []string
		for _, c := range s.ByContinent(results) {
			cells = append(cells, fmt.Sprintf("%s %d/%d", c.Continent, c.InSync, c.Total))
		}
		if len(cells) > 0 {
			p.printf("  in sync: %s\n", strings.Join(cells, "  "))
		}

		sorted := append([]dnsq.Result(nil), results...)
		sort.SliceStable(sorted, func(i, j int) bool {
			a, b := sorted[i], sorted[j]
			if a.Server.Continent != b.Server.Continent {
				return geo.SortKey(a.Server.Continent) < geo.SortKey(b.Server.Continent)
			}
			if a.Server.CountryCode != b.Server.CountryCode {
				return a.Server.CountryCode < b.Server.CountryCode
			}
			return a.Server.Location() < b.Server.Location()
		})

		p.printf("\n  %-7s %-16s %-16s %-30s %5s\n", "REGION", "LOCATION", "RESOLVER", "ANSWER", "MS")
		for _, r := range sorted {
			region := r.Server.Continent
			if r.Server.CountryCode != "" && r.Server.CountryCode != r.Server.Continent {
				region += "/" + r.Server.CountryCode
			}
			rtt := "-"
			if r.RTT > 0 && !r.Failed() {
				rtt = fmt.Sprintf("%d", r.RTT.Milliseconds())
			}
			p.printf("  %-7s %-16s %-16s %-30s %5s\n",
				region,
				truncate(r.Server.Location(), 16),
				truncate(r.Server.IP, 16),
				truncate(r.Key(), 30),
				rtt,
			)
		}
	}
	return run, p.err
}

type jsonRun struct {
	Domain    string     `json:"domain"`
	QueriedAt time.Time  `json:"queried_at"`
	ElapsedMS int64      `json:"elapsed_ms"`
	Resolvers int        `json:"resolvers"`
	Expected  []string   `json:"expected,omitempty"`
	Types     []jsonType `json:"types"`
}

type jsonType struct {
	Type        string       `json:"type"`
	Total       int          `json:"total"`
	Answered    int          `json:"answered"`
	Failed      int          `json:"failed"`
	Consensus   string       `json:"consensus"`
	Propagation float64      `json:"propagation"`
	Groups      []jsonGroup  `json:"groups"`
	Results     []jsonResult `json:"results"`
}

type jsonGroup struct {
	Answer      string         `json:"answer"`
	Status      string         `json:"status"`
	Count       int            `json:"count"`
	Share       float64        `json:"share"`
	Matches     bool           `json:"matches_expected,omitempty"`
	ByContinent map[string]int `json:"by_continent"`
}

type jsonResult struct {
	Resolver    string   `json:"resolver"`
	Name        string   `json:"name,omitempty"`
	ASOrg       string   `json:"as_org,omitempty"`
	Continent   string   `json:"continent"`
	Country     string   `json:"country"`
	City        string   `json:"city,omitempty"`
	Status      string   `json:"status"`
	Answers     []string `json:"answers,omitempty"`
	Records     []string `json:"records,omitempty"`
	InConsensus bool     `json:"in_consensus"`
	RTTMS       int64    `json:"rtt_ms"`
	Error       string   `json:"error,omitempty"`
}

func JSON(ctx context.Context, w io.Writer, opts Options) (Run, error) {
	start := time.Now()
	run, err := Collect(ctx, opts)
	if err != nil {
		return run, err
	}

	doc := jsonRun{
		Domain:    opts.Query.Domain,
		QueriedAt: start.UTC(),
		ElapsedMS: run.Elapsed.Milliseconds(),
		Resolvers: run.Total,
		Expected:  opts.Expected,
	}

	for _, typ := range opts.Query.Types {
		s := run.Summaries[typ]
		jt := jsonType{
			Type:        typ,
			Total:       s.Total,
			Answered:    s.Done,
			Failed:      s.Failed,
			Consensus:   s.Consensus,
			Propagation: round(s.Propagation()),
		}
		for _, g := range s.Groups {
			jt.Groups = append(jt.Groups, jsonGroup{
				Answer:      g.Key,
				Status:      string(g.Status),
				Count:       g.Count,
				Share:       round(g.Share),
				Matches:     g.Matches,
				ByContinent: g.ByContinent,
			})
		}
		for _, r := range run.Results[typ] {
			jr := jsonResult{
				Resolver:    r.Server.IP,
				Name:        r.Server.Name,
				ASOrg:       r.Server.ASOrg,
				Continent:   r.Server.Continent,
				Country:     r.Server.CountryCode,
				City:        r.Server.City,
				Status:      string(r.Status),
				Answers:     r.Answers,
				Records:     r.Records,
				InConsensus: s.InConsensus(r),
				RTTMS:       r.RTT.Milliseconds(),
			}
			if r.Err != nil {
				jr.Error = r.Err.Error()
			}
			jt.Results = append(jt.Results, jr)
		}
		doc.Types = append(doc.Types, jt)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return run, enc.Encode(doc)
}

func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w || w <= 0 {
		return s
	}
	if w == 1 {
		return string(r[:1])
	}
	return string(r[:w-1]) + "…"
}

func pct(share float64) string {
	return fmt.Sprintf("%d%%", int(share*100+0.5))
}

func round(f float64) float64 {
	return float64(int(f*10000+0.5)) / 10000
}

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
