package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/sratabix/multidig/internal/dnsq"
	"github.com/sratabix/multidig/internal/geo"
	"github.com/sratabix/multidig/internal/health"
	"github.com/sratabix/multidig/internal/output"
	"github.com/sratabix/multidig/internal/servers"
	"github.com/sratabix/multidig/internal/tui"
)

var version = "dev"

var errNotPropagated = errors.New("not propagated yet")

type options struct {
	types          string
	continents     string
	perContinent   int
	minReliability float64
	maxAge         time.Duration
	timeout        time.Duration
	retries        int
	concurrency    int
	expect         string
	watch          time.Duration
	explicit       string
	addresses      bool
	includeIPv6    bool
	requireCity    bool
	refresh        bool
	cacheTTL       time.Duration
	format         string
	showVersion    bool

	oversample       int
	noProbe          bool
	reprobe          bool
	probeTimeout     time.Duration
	probeConcurrency int
}

func main() {
	err := run()
	switch {
	case err == nil:
	case errors.Is(err, errNotPropagated):
		os.Exit(2)
	case errors.Is(err, context.Canceled):
		os.Exit(130)
	default:
		fmt.Fprintln(os.Stderr, "multidig: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var o options

	fs := flag.NewFlagSet("multidig", flag.ContinueOnError)
	fs.Usage = usage(fs)

	strVar(fs, &o.types, "A", "types", "t")
	strVar(fs, &o.continents, strings.Join(geo.Default, ","), "continents", "c")
	intVar(fs, &o.perContinent, 6, "per-continent", "n")
	fs.Float64Var(&o.minReliability, "min-reliability", 0.9, "minimum resolver reliability (0-1)")
	fs.DurationVar(&o.maxAge, "max-age", 0, "ignore resolvers not checked within this period (0 disables; the source's checked_at is unreliable)")
	fs.DurationVar(&o.timeout, "timeout", 3*time.Second, "per-query timeout")
	fs.IntVar(&o.retries, "retries", 1, "retries per query")
	fs.IntVar(&o.concurrency, "concurrency", 32, "concurrent queries")
	fs.StringVar(&o.expect, "expect", "", "expected answer(s); propagation is measured against these")
	durVar(fs, &o.watch, 0, "watch", "w")
	strVar(fs, &o.explicit, "", "servers", "s")
	fs.BoolVar(&o.addresses, "addresses", false, "for CNAMEd names compare the resolved addresses instead of the CNAME target")
	fs.BoolVar(&o.includeIPv6, "ipv6", false, "also use IPv6 resolvers")
	fs.BoolVar(&o.requireCity, "require-city", false, "only use resolvers with a known city")
	fs.BoolVar(&o.refresh, "refresh", false, "force a refresh of the resolver list")
	fs.DurationVar(&o.cacheTTL, "cache-ttl", 24*time.Hour, "how long the cached resolver list stays valid")
	fs.IntVar(&o.oversample, "oversample", 12, "candidates to consider per wanted resolver")
	fs.BoolVar(&o.noProbe, "no-probe", false, "skip the reachability probe and trust the published list")
	fs.BoolVar(&o.reprobe, "reprobe", false, "discard cached resolver health and probe again")
	fs.DurationVar(&o.probeTimeout, "probe-timeout", 2*time.Second, "reachability probe timeout")
	fs.IntVar(&o.probeConcurrency, "probe-concurrency", 96, "concurrent reachability probes")
	fs.StringVar(&o.format, "format", "tui", "output format: tui, plain or json")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")

	if err := fs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if o.showVersion {
		fmt.Println("multidig " + version)
		return nil
	}

	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("expected exactly one domain")
	}
	domain := strings.TrimSuffix(strings.TrimSpace(fs.Arg(0)), ".")
	if domain == "" {
		return errors.New("empty domain")
	}

	types, err := parseTypes(o.types)
	if err != nil {
		return err
	}
	continents, err := parseContinents(o.continents)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, note, err := resolvers(ctx, o, continents)
	if err != nil {
		return err
	}
	if len(pool) == 0 {
		return errors.New("no resolvers matched the given filters; try --min-reliability 0.5 or more continents")
	}

	cfg := tui.Config{
		Domain:      domain,
		Types:       types,
		Servers:     pool,
		Timeout:     o.timeout,
		Retries:     o.retries,
		Concurrency: o.concurrency,
		Expected:    parseExpect(o.expect),
		WatchEvery:  o.watch,
		SourceNote:  note,
		Addresses:   o.addresses,
	}

	outOpts := output.Options{Query: cfg.QueryOptions(), Expected: cfg.Expected, Note: note}

	format := strings.ToLower(o.format)
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "format" {
			explicit = true
		}
	})
	if !explicit && format == "tui" && !term.IsTerminal(os.Stdout.Fd()) {
		format = "plain"
	}

	switch format {
	case "json":
		run, err := output.JSON(ctx, os.Stdout, outOpts)
		if err != nil {
			return err
		}
		return propagationStatus(cfg.Expected, run)
	case "plain", "text":
		run, err := output.Plain(ctx, os.Stdout, outOpts)
		if err != nil {
			return err
		}
		return propagationStatus(cfg.Expected, run)
	case "tui":
		model := tui.New(cfg)
		p := tea.NewProgram(model, tea.WithContext(ctx))
		if _, err := p.Run(); err != nil {
			if errors.Is(err, tea.ErrInterrupted) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown format %q (want tui, plain or json)", o.format)
	}
}

func propagationStatus(expected []string, run output.Run) error {
	if len(expected) > 0 && !run.FullyPropagated() {
		return errNotPropagated
	}
	return nil
}

func resolvers(ctx context.Context, o options, continents []string) ([]servers.Server, string, error) {
	if o.explicit != "" {
		list := servers.Explicit(strings.Split(o.explicit, ","))
		return list, fmt.Sprintf("%d custom resolvers", len(list)), nil
	}

	res, err := servers.Load(ctx, servers.LoadOptions{CacheTTL: o.cacheTTL, Refresh: o.refresh})
	if err != nil {
		return nil, "", err
	}

	selectOpts := servers.SelectOptions{
		Continents:     continents,
		PerContinent:   o.perContinent,
		MinReliability: o.minReliability,
		MaxAge:         o.maxAge,
		IncludeIPv6:    o.includeIPv6,
		RequireCity:    o.requireCity,
		Oversample:     o.oversample,
	}

	age := "just now"
	if d := time.Since(res.FetchedAt); d > time.Minute {
		age = shortAge(d) + " old"
	}

	if o.noProbe {
		pool := servers.Select(res.Servers, selectOpts)
		return pool, fmt.Sprintf("public-dns.info · %d resolvers · unprobed · list %s", len(pool), age), nil
	}

	candidates := servers.Candidates(res.Servers, selectOpts)
	pool, stats, err := health.Ensure(ctx, candidates, health.Options{
		PerContinent: o.perContinent,
		Timeout:      o.probeTimeout,
		Concurrency:  o.probeConcurrency,
		Refresh:      o.reprobe,
		Progress:     probeProgress(),
	})
	if err != nil {
		return nil, "", err
	}
	clearProgress()

	note := fmt.Sprintf("public-dns.info · %d resolvers · %d probed, %d cached · list %s",
		len(pool), stats.Probed, stats.FromCache, age)
	return pool, note, nil
}

func probeProgress() func(probed, healthy, want int) {
	if !term.IsTerminal(os.Stderr.Fd()) {
		return nil
	}
	return func(probed, healthy, want int) {
		fmt.Fprintf(os.Stderr, "\r\033[Kfinding reachable resolvers… %d/%d ready (%d probed)", healthy, want, probed)
	}
}

func clearProgress() {
	if term.IsTerminal(os.Stderr.Fd()) {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
}

func parseTypes(in string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(in, ",") {
		t := strings.ToUpper(strings.TrimSpace(part))
		if t == "" {
			continue
		}
		if _, err := dnsq.ParseType(t); err != nil {
			return nil, err
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no record types given")
	}
	return out, nil
}

func parseContinents(in string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(in, ",") {
		c := strings.ToUpper(strings.TrimSpace(part))
		if c == "" {
			continue
		}
		if !geo.Valid(c) {
			return nil, fmt.Errorf("unknown continent %q (use AF, AN, AS, EU, NA, OC, SA)", c)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("no continents given")
	}
	return out, nil
}

func parseExpect(in string) []string {
	var out []string
	for _, part := range strings.Split(in, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func shortAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func strVar(fs *flag.FlagSet, p *string, def string, name, alias string) {
	fs.StringVar(p, name, def, "")
	fs.StringVar(p, alias, def, "")
}

func intVar(fs *flag.FlagSet, p *int, def int, name, alias string) {
	fs.IntVar(p, name, def, "")
	fs.IntVar(p, alias, def, "")
}

func durVar(fs *flag.FlagSet, p *time.Duration, def time.Duration, name, alias string) {
	fs.DurationVar(p, name, def, "")
	fs.DurationVar(p, alias, def, "")
}

func usage(fs *flag.FlagSet) func() {
	return func() {
		out := fs.Output()
		_, _ = fmt.Fprint(out, `multidig — see how far a DNS change has propagated

usage:
  multidig [flags] <domain>

examples:
  multidig example.com
  multidig -t A,AAAA,MX example.com
  multidig --expect 203.0.113.10 -w 10s example.com
  multidig -c EU,NA -n 10 --format plain example.com
  multidig -s 8.8.8.8,1.1.1.1 example.com

flags:
  -t, --types            record types, comma separated (default A)
  -c, --continents       continents to query (default EU,NA,SA,AS,AF,OC)
  -n, --per-continent    resolvers per continent (default 6)
  -s, --servers          query these resolvers instead of the geo pool
  -w, --watch            re-run on this interval, e.g. 10s
      --expect           expected answer(s); propagation is measured against these
      --min-reliability  minimum resolver reliability, 0-1 (default 0.9)
      --oversample       candidates considered per wanted resolver (default 12)
      --no-probe         skip the reachability probe, trust the published list
      --reprobe          discard cached resolver health and probe again
      --probe-timeout    reachability probe timeout (default 2s)
      --max-age          ignore resolvers not checked recently (default off)
      --timeout          per-query timeout (default 3s)
      --retries          retries per query (default 1)
      --concurrency      concurrent queries (default 32)
      --addresses        for CNAMEd names compare resolved addresses, not the
                         CNAME target
      --ipv6             also use IPv6 resolvers
      --require-city     only use resolvers with a known city
      --refresh          force a refresh of the resolver list
      --cache-ttl        resolver list cache lifetime (default 24h)
      --format           tui, plain or json (default tui)
      --version          print version

keys in the tui:
  ↑↓/jk move   enter detail   tab switch record type   / filter
  a answer · r region · t latency · s resolver sort
  R rerun   w toggle watch   q quit
`)
	}
}
