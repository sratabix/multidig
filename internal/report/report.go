package report

import (
	"sort"
	"strings"

	"github.com/sratabix/multidig/internal/dnsq"
	"github.com/sratabix/multidig/internal/geo"
)

type Group struct {
	Key         string
	Status      dnsq.Status
	Count       int
	Share       float64
	Matches     bool
	ByContinent map[string]int
}

type Summary struct {
	Type      string
	Total     int
	Done      int
	Groups    []Group
	Consensus string
	Matched   int
	Failed    int
	Expected  []string
}

func Summarize(typ string, total int, results []dnsq.Result, expected []string) Summary {
	s := Summary{Type: typ, Total: total, Done: len(results), Expected: expected}

	index := make(map[string]*Group)
	for _, r := range results {
		key := r.Key()
		g, ok := index[key]
		if !ok {
			g = &Group{Key: key, Status: r.Status, ByContinent: map[string]int{}}
			index[key] = g
		}
		g.Count++
		g.ByContinent[r.Server.Continent]++
		if r.Failed() {
			s.Failed++
		}
	}

	for _, g := range index {
		if len(expected) > 0 {
			g.Matches = g.Status == dnsq.StatusOK && sameSet(strings.Split(g.Key, ", "), expected)
			if g.Matches {
				s.Matched += g.Count
			}
		}
		if s.Done > 0 {
			g.Share = float64(g.Count) / float64(s.Done)
		}
		s.Groups = append(s.Groups, *g)
	}

	sort.Slice(s.Groups, func(i, j int) bool {
		a, b := s.Groups[i], s.Groups[j]
		if a.Matches != b.Matches {
			return a.Matches
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if (a.Status == dnsq.StatusOK) != (b.Status == dnsq.StatusOK) {
			return a.Status == dnsq.StatusOK
		}
		return a.Key < b.Key
	})

	if len(expected) == 0 {
		for _, g := range s.Groups {
			if g.Status == dnsq.StatusOK {
				s.Consensus = g.Key
				break
			}
		}
		if s.Consensus == "" && len(s.Groups) > 0 {
			s.Consensus = s.Groups[0].Key
		}
	} else {
		s.Consensus = strings.Join(expected, ", ")
	}

	return s
}

func (s Summary) Propagation() float64 {
	if s.Done == 0 {
		return 0
	}
	if len(s.Expected) > 0 {
		return float64(s.Matched) / float64(s.Done)
	}
	for _, g := range s.Groups {
		if g.Key == s.Consensus {
			return g.Share
		}
	}
	return 0
}

func (s Summary) InConsensus(r dnsq.Result) bool {
	return r.Key() == s.Consensus
}

type ContinentStat struct {
	Continent string
	InSync    int
	Total     int
}

func (s Summary) ByContinent(results []dnsq.Result) []ContinentStat {
	counts := map[string]*ContinentStat{}
	for _, r := range results {
		c, ok := counts[r.Server.Continent]
		if !ok {
			c = &ContinentStat{Continent: r.Server.Continent}
			counts[r.Server.Continent] = c
		}
		c.Total++
		if s.InConsensus(r) {
			c.InSync++
		}
	}
	out := make([]ContinentStat, 0, len(counts))
	for _, c := range counts {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		return geo.SortKey(out[i].Continent) < geo.SortKey(out[j].Continent)
	})
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if !strings.EqualFold(strings.TrimSpace(x[i]), strings.TrimSpace(y[i])) {
			return false
		}
	}
	return true
}
