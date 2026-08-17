package servers

import (
	"strings"
	"testing"
)

const sample = `ip_address,name,as_number,as_org,country_code,city,version,error,dnssec,reliability,checked_at,created_at
8.8.8.8,dns.google.,15169,GOOGLE,US,,,,true,1.00,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
1.1.1.1,one.one.one.one.,13335,"CLOUDFLARENET, Inc.",US,Miami,,,true,1.00,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
185.12.64.1,ns1.example.nl.,1136,KPN,NL,Amsterdam,,,true,0.99,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
91.150.92.232,router.home.,8400,Telekom Srbija,RS,Belgrade,,,false,0.95,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
203.0.113.9,,64500,Broken Net,NL,Rotterdam,,connection timed out,false,1.00,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
198.51.100.7,,64501,Unmapped,ZZ,Nowhere,,,false,1.00,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
not-an-ip,,64502,Bad,NL,Utrecht,,,false,1.00,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
2001:4860:4860::8888,dns.google.,15169,GOOGLE,US,,,,true,1.00,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
190.11.225.2,ns.laceiba.hn.,64503,Cablecolor,HN,La Ceiba,,,false,0.93,2023-04-17T08:03:50Z,2020-07-16T14:19:04Z
`

func parsed(t *testing.T) []Server {
	t.Helper()
	list, err := parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return list
}

func TestParseSkipsUnusableRows(t *testing.T) {
	list := parsed(t)

	byIP := map[string]Server{}
	for _, s := range list {
		byIP[s.IP] = s
	}

	for _, skipped := range []string{"203.0.113.9", "198.51.100.7", "not-an-ip"} {
		if _, ok := byIP[skipped]; ok {
			t.Errorf("%s should have been skipped", skipped)
		}
	}
	if len(list) != 6 {
		t.Fatalf("parsed %d rows, want 6: %v", len(list), byIP)
	}

	nl := byIP["185.12.64.1"]
	if nl.Continent != "EU" || nl.City != "Amsterdam" || nl.Name != "ns1.example.nl" {
		t.Errorf("unexpected NL row: %+v", nl)
	}
	if cf := byIP["1.1.1.1"]; cf.ASOrg != "CLOUDFLARENET, Inc." {
		t.Errorf("quoted as_org parsed as %q", cf.ASOrg)
	}
}

func TestSelectExcludesIPv6ByDefault(t *testing.T) {
	list := parsed(t)

	got := Select(list, SelectOptions{Continents: []string{"NA", "EU"}, PerContinent: 4, MinReliability: 0.9})
	for _, s := range got {
		if s.IsIPv6() {
			t.Errorf("IPv6 resolver %s selected without --ipv6", s.IP)
		}
	}

	withV6 := Select(list, SelectOptions{Continents: []string{"NA", "EU"}, PerContinent: 4, MinReliability: 0.9, IncludeIPv6: true})
	found := false
	for _, s := range withV6 {
		if s.IsIPv6() {
			found = true
		}
	}
	if !found {
		t.Error("IPv6 resolver missing even with IncludeIPv6")
	}
}

func TestSelectSpreadsAcrossCountries(t *testing.T) {
	list := parsed(t)
	got := Select(list, SelectOptions{Continents: []string{"NA"}, PerContinent: 2, MinReliability: 0.9})
	if len(got) != 2 {
		t.Fatalf("selected %d, want 2", len(got))
	}
	if got[0].CountryCode == got[1].CountryCode {
		t.Errorf("both picks from %s; countries should be spread", got[0].CountryCode)
	}
}

func TestSelectHonoursMinReliability(t *testing.T) {
	list := parsed(t)
	got := Select(list, SelectOptions{Continents: []string{"NA"}, PerContinent: 6, MinReliability: 0.99})
	for _, s := range got {
		if s.Reliability < 0.99 {
			t.Errorf("%s has reliability %.2f", s.IP, s.Reliability)
		}
	}
}

func TestScorePrefersRealNameservers(t *testing.T) {
	list := parsed(t)
	byIP := map[string]Server{}
	for _, s := range list {
		byIP[s.IP] = s
	}
	ns := byIP["185.12.64.1"]
	router := byIP["91.150.92.232"]
	if score(ns) <= score(router) {
		t.Errorf("nameserver-looking host scored %.2f, router scored %.2f", score(ns), score(router))
	}
}

func TestCandidatesOversamples(t *testing.T) {
	list := parsed(t)
	cands := Candidates(list, SelectOptions{Continents: []string{"NA"}, PerContinent: 1, MinReliability: 0.9, Oversample: 5})
	if len(cands["NA"]) < 2 {
		t.Errorf("oversampling returned %d candidates, want more than 1", len(cands["NA"]))
	}
}
