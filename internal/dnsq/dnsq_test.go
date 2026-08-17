package dnsq

import (
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func rrs(t *testing.T, lines ...string) []dns.RR {
	t.Helper()
	out := make([]dns.RR, 0, len(lines))
	for _, l := range lines {
		rr, err := dns.NewRR(l)
		if err != nil {
			t.Fatalf("parse %q: %v", l, err)
		}
		out = append(out, rr)
	}
	return out
}

func TestAnswerSetSortsAndDedupes(t *testing.T) {
	answers, records := answerSet(rrs(t,
		"example.com. 300 IN A 9.9.9.9",
		"example.com. 300 IN A 1.1.1.1",
		"example.com. 300 IN A 9.9.9.9",
	), "example.com", dns.TypeA, false)

	if got := strings.Join(answers, ","); got != "1.1.1.1,9.9.9.9" {
		t.Errorf("answers = %q, want a sorted deduped set", got)
	}
	if len(records) != 3 {
		t.Errorf("records = %v, want the full answer section", records)
	}
}

func TestAnswerSetReportsCNAMETargetNotAddresses(t *testing.T) {
	answers, records := answerSet(rrs(t,
		"www.texel.net. 26 IN CNAME ingress.travelbase.nl.",
		"ingress.travelbase.nl. 60 IN A 18.239.83.122",
		"ingress.travelbase.nl. 60 IN A 18.239.83.104",
		"ingress.travelbase.nl. 60 IN A 18.239.83.35",
	), "www.texel.net", dns.TypeA, false)

	if len(answers) != 1 || answers[0] != "CNAME ingress.travelbase.nl" {
		t.Errorf("answers = %v, want the CNAME target", answers)
	}
	want := "CNAME ingress.travelbase.nl · A 18.239.83.122 · A 18.239.83.104 · A 18.239.83.35"
	if got := strings.Join(records, " · "); got != want {
		t.Errorf("records = %q, want the full chain in answer order", got)
	}
}

func TestAnswerSetFollowsCNAMEChainToTheEnd(t *testing.T) {
	answers, _ := answerSet(rrs(t,
		"a.example.com. 300 IN CNAME b.example.com.",
		"b.example.com. 300 IN CNAME c.example.net.",
		"c.example.net. 300 IN A 1.1.1.1",
	), "a.example.com", dns.TypeA, false)

	if len(answers) != 1 || answers[0] != "CNAME c.example.net" {
		t.Errorf("answers = %v, want the terminal target of the chain", answers)
	}
}

func TestAnswerSetChainWithoutQueryNameOwner(t *testing.T) {
	answers, _ := answerSet(rrs(t,
		"other.example.com. 300 IN CNAME target.example.net.",
	), "www.example.com", dns.TypeA, false)

	if len(answers) != 1 || answers[0] != "CNAME target.example.net" {
		t.Errorf("answers = %v, want a target even when the chain does not start at the query name", answers)
	}
}

func TestAnswerSetAddressesModePrefersAddresses(t *testing.T) {
	answers, _ := answerSet(rrs(t,
		"www.texel.net. 26 IN CNAME ingress.travelbase.nl.",
		"ingress.travelbase.nl. 60 IN A 18.239.83.35",
		"ingress.travelbase.nl. 60 IN A 18.239.83.33",
	), "www.texel.net", dns.TypeA, true)

	if got := strings.Join(answers, ","); got != "18.239.83.33,18.239.83.35" {
		t.Errorf("answers = %q, want the addresses in --addresses mode", got)
	}
}

func TestAnswerSetKeepsCNAMEForCNAMEQueries(t *testing.T) {
	answers, _ := answerSet(rrs(t, "www.example.com. 300 IN CNAME example.com."), "www.example.com", dns.TypeCNAME, false)
	if len(answers) != 1 || answers[0] != "example.com" {
		t.Errorf("answers = %v, want the bare target without a CNAME prefix", answers)
	}
}

func TestFormatRecordTypes(t *testing.T) {
	cases := map[string]string{
		"example.com. 300 IN MX 10 mail.example.com.":                            "10 mail.example.com",
		"example.com. 300 IN TXT \"v=spf1\" \" -all\"":                           "v=spf1 -all",
		"example.com. 300 IN CAA 0 issue \"letsencrypt.org\"":                    "0 issue letsencrypt.org",
		"_sip._tcp.example.com. 300 IN SRV 10 60 5060 sip.example.com.":          "10 60 5060 sip.example.com",
		"example.com. 300 IN NS ns1.example.com.":                                "ns1.example.com",
		"example.com. 300 IN SOA ns1.example.com. admin.example.com. 42 1 1 1 1": "ns1.example.com admin.example.com 42",
		"example.com. 300 IN AAAA 2001:db8::1":                                   "2001:db8::1",
	}

	for line, want := range cases {
		got := format(rrs(t, line)[0])
		if got != want {
			t.Errorf("format(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestResultKeyPerStatus(t *testing.T) {
	ok := Result{Status: StatusOK, Answers: []string{"1.1.1.1", "9.9.9.9"}}
	if ok.Key() != "1.1.1.1, 9.9.9.9" {
		t.Errorf("ok key = %q", ok.Key())
	}
	if ok.Failed() {
		t.Error("StatusOK must not count as failed")
	}
	if (Result{Status: StatusEmpty}).Failed() {
		t.Error("an empty answer is a real reply, not a failure")
	}
	if !(Result{Status: StatusTimeout}).Failed() {
		t.Error("timeout must count as failed")
	}
}

func TestParseTypeRejectsGarbage(t *testing.T) {
	if _, err := ParseType("NOPE"); err == nil {
		t.Error("expected an error for an unknown record type")
	}
	if got, err := ParseType(" aaaa "); err != nil || got != dns.TypeAAAA {
		t.Errorf("ParseType(\" aaaa \") = %v, %v", got, err)
	}
}
