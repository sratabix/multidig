package health

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/sratabix/multidig/internal/geo"
	"github.com/sratabix/multidig/internal/servers"
)

const (
	probeName  = "example.com."
	goodTTL    = 7 * 24 * time.Hour
	badTTL     = 21 * 24 * time.Hour
	cacheState = 1
)

type entry struct {
	OK      bool      `json:"ok"`
	RTTMS   int64     `json:"rtt_ms"`
	Checked time.Time `json:"checked"`
}

type Cache struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`

	path  string
	mu    sync.Mutex
	dirty bool
}

func cachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "multidig", "health.json"), nil
}

func LoadCache() *Cache {
	c := &Cache{Version: cacheState, Entries: map[string]entry{}}
	path, err := cachePath()
	if err != nil {
		return c
	}
	c.path = path

	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var loaded Cache
	if err := json.Unmarshal(data, &loaded); err != nil || loaded.Version != cacheState {
		return c
	}
	if loaded.Entries != nil {
		c.Entries = loaded.Entries
	}
	return c
}

func (c *Cache) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty || c.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	c.dirty = false
	return os.Rename(tmp, c.path)
}

func (c *Cache) lookup(ip string) (entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.Entries[ip]
	if !ok {
		return entry{}, false
	}
	ttl := badTTL
	if e.OK {
		ttl = goodTTL
	}
	if time.Since(e.Checked) > ttl {
		return entry{}, false
	}
	return e, true
}

func (c *Cache) store(ip string, e entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Entries[ip] = e
	c.dirty = true
}

func (c *Cache) Prune(keep time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ip, e := range c.Entries {
		if time.Since(e.Checked) > keep {
			delete(c.Entries, ip)
			c.dirty = true
		}
	}
}

type Options struct {
	PerContinent int
	Timeout      time.Duration
	Concurrency  int
	Refresh      bool
	Progress     func(probed, healthy, want int)
}

type Stats struct {
	Probed      int
	FromCache   int
	Unreachable int
}

func Ensure(ctx context.Context, candidates map[string][]servers.Server, opts Options) ([]servers.Server, Stats, error) {
	if opts.PerContinent <= 0 {
		opts.PerContinent = 6
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 64
	}

	cache := LoadCache()
	if opts.Refresh {
		cache.Entries = map[string]entry{}
		cache.dirty = true
	}
	cache.Prune(90 * 24 * time.Hour)

	var (
		stats  Stats
		result = map[string][]servers.Server{}
		want   = opts.PerContinent * len(candidates)
	)

	pending := map[string][]servers.Server{}
	for continent, list := range candidates {
		for _, s := range list {
			if len(result[continent]) >= opts.PerContinent {
				break
			}
			e, ok := cache.lookup(s.IP)
			if !ok {
				pending[continent] = append(pending[continent], s)
				continue
			}
			stats.FromCache++
			if e.OK {
				result[continent] = append(result[continent], s)
			} else {
				stats.Unreachable++
			}
		}
	}

	healthy := 0
	for _, list := range result {
		healthy += len(list)
	}
	if opts.Progress != nil {
		opts.Progress(0, healthy, want)
	}

	for round := 0; ; round++ {
		batch := make([]servers.Server, 0, opts.Concurrency)
		for continent, list := range pending {
			need := opts.PerContinent - len(result[continent])
			if need <= 0 || len(list) == 0 {
				continue
			}
			take := min(need*3, len(list))
			batch = append(batch, list[:take]...)
			pending[continent] = list[take:]
		}
		if len(batch) == 0 {
			break
		}
		if ctx.Err() != nil {
			break
		}

		for _, r := range probe(ctx, batch, opts) {
			stats.Probed++
			cache.store(r.server.IP, entry{OK: r.ok, RTTMS: r.rtt.Milliseconds(), Checked: time.Now()})
			if !r.ok {
				stats.Unreachable++
				continue
			}
			c := r.server.Continent
			if len(result[c]) < opts.PerContinent {
				result[c] = append(result[c], r.server)
				healthy++
			}
		}
		if opts.Progress != nil {
			opts.Progress(stats.Probed, healthy, want)
		}
	}

	_ = cache.Save()

	return flatten(result), stats, ctx.Err()
}

func flatten(byContinent map[string][]servers.Server) []servers.Server {
	var out []servers.Server
	for _, continent := range geo.Order {
		out = append(out, byContinent[continent]...)
	}
	for continent, list := range byContinent {
		if geo.Valid(continent) {
			continue
		}
		out = append(out, list...)
	}
	return out
}

type probeResult struct {
	server servers.Server
	ok     bool
	rtt    time.Duration
}

func probe(ctx context.Context, batch []servers.Server, opts Options) []probeResult {
	jobs := make(chan servers.Server)
	results := make(chan probeResult, len(batch))

	go func() {
		defer close(jobs)
		for _, s := range batch {
			select {
			case jobs <- s:
			case <-ctx.Done():
				return
			}
		}
	}()

	workers := min(opts.Concurrency, len(batch))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &dns.Client{Net: "udp", Timeout: opts.Timeout, UDPSize: 4096}
			msg := new(dns.Msg)
			msg.SetQuestion(probeName, dns.TypeA)
			msg.RecursionDesired = true

			for s := range jobs {
				reply, rtt, err := client.ExchangeContext(ctx, msg, s.Addr())
				ok := err == nil && reply != nil && reply.Rcode == dns.RcodeSuccess && hasA(reply)
				results <- probeResult{server: s, ok: ok, rtt: rtt}
			}
		}()
	}
	wg.Wait()
	close(results)

	out := make([]probeResult, 0, len(batch))
	for r := range results {
		out = append(out, r)
	}
	return out
}

func hasA(msg *dns.Msg) bool {
	for _, rr := range msg.Answer {
		if _, ok := rr.(*dns.A); ok {
			return true
		}
	}
	return false
}
