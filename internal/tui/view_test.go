package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sratabix/multidig/internal/dnsq"
	"github.com/sratabix/multidig/internal/servers"
)

func srv(continent, cc, city, ip string) servers.Server {
	return servers.Server{IP: ip, City: city, CountryCode: cc, Continent: continent, Reliability: 1, ASOrg: "Example Telecom " + cc}
}

func fixture() *Model {
	pool := []servers.Server{
		srv("EU", "NL", "Amsterdam", "185.12.64.1"),
		srv("EU", "DE", "Nuremberg", "159.69.114.157"),
		srv("EU", "RS", "Belgrade", "91.150.92.232"),
		srv("NA", "CA", "Burnaby", "208.91.112.52"),
		srv("NA", "US", "Elk Grove Village", "107.191.48.176"),
		srv("NA", "HN", "La Ceiba", "190.11.225.2"),
		srv("SA", "CL", "La Serena", "186.103.167.190"),
		srv("SA", "PY", "Asunción", "201.217.57.148"),
		srv("AS", "HK", "Tai Kok Tsui", "202.131.73.38"),
		srv("AS", "JP", "Chuo", "219.166.58.130"),
		srv("AF", "TN", "", "196.203.125.132"),
		srv("OC", "AU", "Melbourne", "1.10.10.10"),
	}

	m := New(Config{
		Domain:     "example.com",
		Types:      []string{"A", "MX"},
		Servers:    pool,
		WatchEvery: 10 * time.Second,
		SourceNote: "public-dns.info · 12 resolvers",
	})
	m.width, m.height = 110, 32

	statuses := []struct {
		status  dnsq.Status
		answers []string
		rtt     time.Duration
	}{
		{dnsq.StatusOK, []string{"203.0.113.10"}, 12 * time.Millisecond},
		{dnsq.StatusOK, []string{"203.0.113.10"}, 42 * time.Millisecond},
		{dnsq.StatusOK, []string{"203.0.113.10"}, 64 * time.Millisecond},
		{dnsq.StatusOK, []string{"203.0.113.10"}, 18 * time.Millisecond},
		{dnsq.StatusOK, []string{"203.0.113.10"}, 208 * time.Millisecond},
		{dnsq.StatusOK, []string{"203.0.113.10"}, 156 * time.Millisecond},
		{dnsq.StatusOK, []string{"93.184.216.34"}, 465 * time.Millisecond},
		{dnsq.StatusEmpty, nil, 532 * time.Millisecond},
		{dnsq.StatusOK, []string{"203.0.113.10"}, 266 * time.Millisecond},
		{dnsq.StatusTimeout, nil, 0},
		{dnsq.StatusOK, []string{"203.0.113.10"}, 263 * time.Millisecond},
		{dnsq.StatusRefused, nil, 0},
	}

	for i, s := range pool {
		st := statuses[i]
		m.results["A"] = append(m.results["A"], dnsq.Result{
			Server: s, Type: "A", Status: st.status, Answers: st.answers, RTT: st.rtt,
		})
	}
	m.elapsed = 1250 * time.Millisecond
	m.recompute()
	return m
}

func TestRenderLayout(t *testing.T) {
	m := fixture()
	out := ansi.Strip(m.render())
	t.Log("\n" + out)

	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > m.width {
			t.Errorf("line %d is %d wide, terminal is %d: %q", i, w, m.width, line)
		}
	}

	for _, want := range []string{"multidig", "example.com", "203.0.113.10", "REGION", "Amsterdam", "watch 10s"} {
		if !strings.Contains(out, want) {
			t.Errorf("render is missing %q", want)
		}
	}
}

func TestRenderHeightIsExact(t *testing.T) {
	for _, distinct := range []int{0, 1, 2, 5, 9} {
		for _, height := range []int{24, 40} {
			for _, detail := range []bool{false, true} {
				m := fixture()
				m.height = height
				m.showDetail = detail
				m.results["A"] = m.results["A"][:0]

				for i, s := range m.cfg.Servers {
					if distinct == 0 {
						break
					}
					answer := fmt.Sprintf("10.0.0.%d", i%distinct)
					m.results["A"] = append(m.results["A"], dnsq.Result{
						Server: s, Type: "A", Status: dnsq.StatusOK,
						Answers: []string{answer}, RTT: time.Millisecond,
					})
				}
				m.recompute()

				lines := strings.Count(strings.TrimSuffix(m.render(), "\n"), "\n") + 1
				if lines != height {
					t.Errorf("distinct=%d height=%d detail=%v rendered %d lines", distinct, height, detail, lines)
				}
			}
		}
	}
}

func TestRenderDetailAndFilter(t *testing.T) {
	m := fixture()
	m.showDetail = true
	m.cursor = 1
	out := ansi.Strip(m.render())
	t.Log("\n" + out)
	if !strings.Contains(out, "reliability") {
		t.Error("detail pane missing resolver metadata")
	}

	m.showDetail = false
	m.filter.SetValue("timeout")
	m.recompute()
	if len(m.rows) != 1 {
		t.Fatalf("filter matched %d rows, want 1", len(m.rows))
	}
	if m.rows[0].Server.City != "Chuo" {
		t.Errorf("filter matched %q", m.rows[0].Server.City)
	}
}

func TestTabsKeepRecordTypesSeparate(t *testing.T) {
	m := fixture()
	for _, s := range m.cfg.Servers {
		m.results["MX"] = append(m.results["MX"], dnsq.Result{
			Server: s, Type: "MX", Status: dnsq.StatusOK,
			Answers: []string{"10 mail.example.com"}, RTT: 5 * time.Millisecond,
		})
	}

	m.recompute()
	for _, r := range m.rows {
		if r.Type != "A" {
			t.Fatalf("A tab shows a %s row", r.Type)
		}
	}

	m.activeType = 1
	m.recompute()
	if len(m.rows) != len(m.cfg.Servers) {
		t.Fatalf("MX tab has %d rows, want %d", len(m.rows), len(m.cfg.Servers))
	}
	for _, r := range m.rows {
		if r.Type != "MX" {
			t.Fatalf("MX tab shows a %s row", r.Type)
		}
	}
	if got := m.summaries["MX"].Consensus; got != "10 mail.example.com" {
		t.Errorf("MX consensus = %q", got)
	}
	if got := m.summaries["A"].Consensus; got != "203.0.113.10" {
		t.Errorf("switching tabs changed the A consensus to %q", got)
	}
}

func TestDetailFollowsCursor(t *testing.T) {
	m := fixture()
	m.showDetail = true
	m.cursor = 3

	want := m.rows[3]
	out := ansi.Strip(m.renderDetail(100))
	if !strings.Contains(out, want.Server.IP) {
		t.Errorf("detail pane does not show the cursor row %s:\n%s", want.Server.IP, out)
	}
	for i, r := range m.rows {
		if i == 3 || r.Server.IP == want.Server.IP {
			continue
		}
		if strings.Contains(out, r.Server.IP) {
			t.Errorf("detail pane leaked row %d (%s)", i, r.Server.IP)
		}
	}
}

func TestSummaryConsensus(t *testing.T) {
	m := fixture()
	s := m.summaries["A"]
	if s.Consensus != "203.0.113.10" {
		t.Errorf("consensus = %q", s.Consensus)
	}
	if got := s.Propagation(); got < 0.66 || got > 0.67 {
		t.Errorf("propagation = %.3f, want ~0.667 (8 of 12)", got)
	}
}

func TestNarrowTerminal(t *testing.T) {
	m := fixture()
	m.width = 62
	m.recompute()
	out := ansi.Strip(m.render())
	t.Log("\n" + out)
	for i, line := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(line); w > m.width {
			t.Errorf("line %d is %d wide, terminal is %d: %q", i, w, m.width, line)
		}
	}
}
