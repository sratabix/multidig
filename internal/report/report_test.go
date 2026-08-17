package report

import (
	"testing"

	"github.com/sratabix/multidig/internal/dnsq"
	"github.com/sratabix/multidig/internal/servers"
)

func result(continent, ip string, status dnsq.Status, answers ...string) dnsq.Result {
	return dnsq.Result{
		Server:  servers.Server{IP: ip, Continent: continent},
		Type:    "A",
		Status:  status,
		Answers: answers,
	}
}

func TestConsensusIgnoresFailures(t *testing.T) {
	results := []dnsq.Result{
		result("EU", "a", dnsq.StatusTimeout),
		result("EU", "b", dnsq.StatusTimeout),
		result("EU", "c", dnsq.StatusTimeout),
		result("NA", "d", dnsq.StatusOK, "1.2.3.4"),
		result("NA", "e", dnsq.StatusOK, "1.2.3.4"),
	}

	s := Summarize("A", 5, results, nil)
	if s.Consensus != "1.2.3.4" {
		t.Fatalf("consensus = %q, want the answer rather than the timeout majority", s.Consensus)
	}
	if got := s.Propagation(); got < 0.39 || got > 0.41 {
		t.Errorf("propagation = %.3f, want 0.4", got)
	}
	if s.Failed != 3 {
		t.Errorf("failed = %d, want 3", s.Failed)
	}
}

func TestExpectedOverridesConsensus(t *testing.T) {
	results := []dnsq.Result{
		result("EU", "a", dnsq.StatusOK, "9.9.9.9"),
		result("EU", "b", dnsq.StatusOK, "9.9.9.9"),
		result("EU", "c", dnsq.StatusOK, "9.9.9.9"),
		result("NA", "d", dnsq.StatusOK, "1.2.3.4"),
	}

	s := Summarize("A", 4, results, []string{"1.2.3.4"})
	if s.Consensus != "1.2.3.4" {
		t.Errorf("consensus = %q, want the expected value", s.Consensus)
	}
	if s.Matched != 1 {
		t.Errorf("matched = %d, want 1", s.Matched)
	}
	if got := s.Propagation(); got < 0.24 || got > 0.26 {
		t.Errorf("propagation = %.3f, want 0.25", got)
	}
	if !s.Groups[0].Matches {
		t.Error("the expected answer should sort first")
	}
}

func TestExpectedMatchesRegardlessOfOrder(t *testing.T) {
	results := []dnsq.Result{
		result("EU", "a", dnsq.StatusOK, "1.1.1.1", "2.2.2.2"),
	}
	s := Summarize("A", 1, results, []string{"2.2.2.2", "1.1.1.1"})
	if s.Matched != 1 {
		t.Errorf("matched = %d; multi-value answers should match irrespective of order", s.Matched)
	}
}

func TestByContinentCountsInSync(t *testing.T) {
	results := []dnsq.Result{
		result("EU", "a", dnsq.StatusOK, "1.2.3.4"),
		result("EU", "b", dnsq.StatusOK, "9.9.9.9"),
		result("NA", "c", dnsq.StatusOK, "1.2.3.4"),
	}
	s := Summarize("A", 3, results, nil)

	stats := s.ByContinent(results)
	got := map[string][2]int{}
	for _, c := range stats {
		got[c.Continent] = [2]int{c.InSync, c.Total}
	}
	if got["EU"] != [2]int{1, 2} {
		t.Errorf("EU = %v, want 1/2", got["EU"])
	}
	if got["NA"] != [2]int{1, 1} {
		t.Errorf("NA = %v, want 1/1", got["NA"])
	}
	if len(stats) != 2 || stats[0].Continent != "EU" {
		t.Errorf("continents should be in geographic order, got %+v", stats)
	}
}

func TestEmptyAndFailureKeysAreDistinct(t *testing.T) {
	keys := map[string]bool{}
	for _, r := range []dnsq.Result{
		result("EU", "a", dnsq.StatusEmpty),
		result("EU", "b", dnsq.StatusNXDomain),
		result("EU", "c", dnsq.StatusTimeout),
		result("EU", "d", dnsq.StatusRefused),
		result("EU", "e", dnsq.StatusServFail),
	} {
		if keys[r.Key()] {
			t.Errorf("duplicate key %q", r.Key())
		}
		keys[r.Key()] = true
	}
}
