package servers

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sratabix/multidig/internal/geo"
)

const SourceURL = "https://public-dns.info/nameservers.csv"

type Server struct {
	IP          string
	Name        string
	ASOrg       string
	CountryCode string
	City        string
	Continent   string
	DNSSEC      bool
	Reliability float64
	CheckedAt   time.Time
}

func (s Server) Location() string {
	if s.City != "" {
		return s.City
	}
	return s.CountryCode
}

func (s Server) Addr() string {
	return net.JoinHostPort(s.IP, "53")
}

func (s Server) IsIPv6() bool {
	ip := net.ParseIP(s.IP)
	return ip != nil && ip.To4() == nil
}

type LoadOptions struct {
	CacheTTL time.Duration
	Refresh  bool
	Timeout  time.Duration
}

type LoadResult struct {
	Servers   []Server
	CachePath string
	FetchedAt time.Time
	FromCache bool
}

func CachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "multidig", "nameservers.csv"), nil
}

func Load(ctx context.Context, opts LoadOptions) (LoadResult, error) {
	if opts.CacheTTL == 0 {
		opts.CacheTTL = 24 * time.Hour
	}
	if opts.Timeout == 0 {
		opts.Timeout = 60 * time.Second
	}

	path, err := CachePath()
	if err != nil {
		return LoadResult{}, fmt.Errorf("locate cache dir: %w", err)
	}

	var (
		cached    bool
		usable    bool
		fetchedAt time.Time
	)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		cached = true
		fetchedAt = info.ModTime()
		usable = time.Since(info.ModTime()) < opts.CacheTTL
	}

	fromCache := true
	if opts.Refresh || !usable {
		if err := download(ctx, path, opts.Timeout); err != nil {
			if !cached {
				return LoadResult{}, err
			}
		} else {
			fetchedAt = time.Now()
			fromCache = false
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return LoadResult{}, fmt.Errorf("open server list: %w", err)
	}
	defer func() { _ = f.Close() }()

	list, err := parse(f)
	if err != nil {
		return LoadResult{}, err
	}
	if len(list) == 0 {
		return LoadResult{}, errors.New("server list is empty")
	}

	return LoadResult{Servers: list, CachePath: path, FetchedAt: fetchedAt, FromCache: fromCache}, nil
}

func download(ctx context.Context, path string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "multidig")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", SourceURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: unexpected status %s", SourceURL, resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "nameservers-*.csv")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func parse(r io.Reader) ([]Server, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.ReuseRecord = true
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read server list header: %w", err)
	}
	idx := make(map[string]int, len(header))
	for i, name := range header {
		idx[strings.TrimSpace(strings.ToLower(name))] = i
	}
	get := func(rec []string, key string) string {
		i, ok := idx[key]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	out := make([]Server, 0, 32768)
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, csv.ErrFieldCount) {
				continue
			}
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				continue
			}
			return nil, err
		}
		if get(rec, "error") != "" {
			continue
		}
		ip := get(rec, "ip_address")
		if net.ParseIP(ip) == nil {
			continue
		}
		cc := strings.ToUpper(get(rec, "country_code"))
		continent, ok := geo.Continent(cc)
		if !ok {
			continue
		}
		reliability, _ := strconv.ParseFloat(get(rec, "reliability"), 64)
		checked, _ := time.Parse(time.RFC3339, get(rec, "checked_at"))

		out = append(out, Server{
			IP:          ip,
			Name:        strings.TrimSuffix(get(rec, "name"), "."),
			ASOrg:       get(rec, "as_org"),
			CountryCode: cc,
			City:        get(rec, "city"),
			Continent:   continent,
			DNSSEC:      get(rec, "dnssec") == "true",
			Reliability: reliability,
			CheckedAt:   checked,
		})
	}
	return out, nil
}

type SelectOptions struct {
	Continents     []string
	PerContinent   int
	MinReliability float64
	MaxAge         time.Duration
	IncludeIPv6    bool
	RequireCity    bool
	Oversample     int
}

var nameserverHints = []string{"dns", "resolver", "resolv", "recursor", "cache", "unbound", "bind"}

func score(s Server) float64 {
	sc := s.Reliability * 2
	if s.Name != "" {
		sc += 0.5
		label := strings.ToLower(s.Name)
		if i := strings.Index(label, "."); i > 0 {
			label = label[:i]
		}
		if strings.HasPrefix(label, "ns") {
			sc += 2
		} else {
			for _, hint := range nameserverHints {
				if strings.Contains(label, hint) {
					sc += 2
					break
				}
			}
		}
	}
	if s.DNSSEC {
		sc++
	}
	if s.City != "" {
		sc += 0.25
	}
	return sc
}

func Select(all []Server, opts SelectOptions) []Server {
	byContinent := Candidates(all, opts)
	perContinent := opts.PerContinent
	if perContinent <= 0 {
		perContinent = 6
	}
	var out []Server
	for _, continent := range geo.Order {
		list := byContinent[continent]
		out = append(out, list[:min(len(list), perContinent)]...)
	}
	return out
}

func Candidates(all []Server, opts SelectOptions) map[string][]Server {
	if opts.PerContinent <= 0 {
		opts.PerContinent = 6
	}
	if opts.Oversample < 1 {
		opts.Oversample = 1
	}
	if len(opts.Continents) == 0 {
		opts.Continents = geo.Default
	}
	wanted := make(map[string]bool, len(opts.Continents))
	for _, c := range opts.Continents {
		wanted[strings.ToUpper(c)] = true
	}

	cutoff := time.Time{}
	if opts.MaxAge > 0 {
		cutoff = time.Now().Add(-opts.MaxAge)
	}
	byContinent := make(map[string]map[string][]Server)
	for _, s := range all {
		if !wanted[s.Continent] {
			continue
		}
		if s.Reliability < opts.MinReliability {
			continue
		}
		if !cutoff.IsZero() && !s.CheckedAt.IsZero() && s.CheckedAt.Before(cutoff) {
			continue
		}
		if !opts.IncludeIPv6 && s.IsIPv6() {
			continue
		}
		if opts.RequireCity && s.City == "" {
			continue
		}
		if byContinent[s.Continent] == nil {
			byContinent[s.Continent] = make(map[string][]Server)
		}
		byContinent[s.Continent][s.CountryCode] = append(byContinent[s.Continent][s.CountryCode], s)
	}

	limit := opts.PerContinent * opts.Oversample
	out := make(map[string][]Server, len(byContinent))

	for continent, countries := range byContinent {
		keys := make([]string, 0, len(countries))
		for cc, list := range countries {
			sort.Slice(list, func(i, j int) bool {
				si, sj := score(list[i]), score(list[j])
				if si != sj {
					return si > sj
				}
				return list[i].IP < list[j].IP
			})
			countries[cc] = list
			keys = append(keys, cc)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := score(countries[keys[i]][0]), score(countries[keys[j]][0])
			if a != b {
				return a > b
			}
			if len(countries[keys[i]]) != len(countries[keys[j]]) {
				return len(countries[keys[i]]) > len(countries[keys[j]])
			}
			return keys[i] < keys[j]
		})

		maxPerOrg := 1
		if opts.Oversample > 1 {
			maxPerOrg = 2
		}
		picked := make([]Server, 0, limit)
		perOrg := make(map[string]int)
		for round := 0; len(picked) < limit; round++ {
			progressed := false
			for _, cc := range keys {
				if len(picked) >= limit {
					break
				}
				list := countries[cc]
				if round >= len(list) {
					continue
				}
				progressed = true
				s := list[round]
				org := strings.ToLower(s.ASOrg)
				if org != "" && perOrg[org] >= maxPerOrg {
					continue
				}
				perOrg[org]++
				picked = append(picked, s)
			}
			if !progressed {
				break
			}
		}
		out[continent] = picked
	}
	return out
}

func Explicit(addrs []string) []Server {
	out := make([]Server, 0, len(addrs))
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		out = append(out, Server{IP: a, City: "custom", CountryCode: "--", Continent: "--", Reliability: 1})
	}
	return out
}
