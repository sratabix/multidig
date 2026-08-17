package dnsq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/sratabix/multidig/internal/servers"
)

type Status string

const (
	StatusOK       Status = "ok"
	StatusEmpty    Status = "empty"
	StatusNXDomain Status = "nxdomain"
	StatusRefused  Status = "refused"
	StatusServFail Status = "servfail"
	StatusTimeout  Status = "timeout"
	StatusError    Status = "error"
)

type Result struct {
	Server  servers.Server
	Type    string
	Status  Status
	Answers []string
	Records []string
	RTT     time.Duration
	Err     error
}

func (r Result) Key() string {
	switch r.Status {
	case StatusOK:
		return strings.Join(r.Answers, ", ")
	case StatusEmpty:
		return "(no records)"
	case StatusNXDomain:
		return "NXDOMAIN"
	case StatusRefused:
		return "REFUSED"
	case StatusServFail:
		return "SERVFAIL"
	case StatusTimeout:
		return "timeout"
	default:
		return "error"
	}
}

func (r Result) Failed() bool {
	return r.Status != StatusOK && r.Status != StatusEmpty
}

var SupportedTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA", "CAA", "SRV", "PTR"}

func ParseType(s string) (uint16, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	t, ok := dns.StringToType[s]
	if !ok {
		return 0, fmt.Errorf("unknown record type %q", s)
	}
	return t, nil
}

type Options struct {
	Domain      string
	Types       []string
	Servers     []servers.Server
	Concurrency int
	Timeout     time.Duration
	Retries     int
	Addresses   bool
}

func Run(ctx context.Context, opts Options) <-chan Result {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 24
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Second
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	}

	type job struct {
		server servers.Server
		typ    string
	}

	jobs := make(chan job)
	out := make(chan Result)

	go func() {
		defer close(jobs)
		for _, t := range opts.Types {
			for _, s := range opts.Servers {
				select {
				case jobs <- job{server: s, typ: t}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	var wg sync.WaitGroup
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				res := query(ctx, j.server, opts.Domain, j.typ, opts.Timeout, opts.Retries, opts.Addresses)
				select {
				case out <- res:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

func query(ctx context.Context, srv servers.Server, domain, typ string, timeout time.Duration, retries int, preferAddresses bool) Result {
	res := Result{Server: srv, Type: typ}

	qtype, err := ParseType(typ)
	if err != nil {
		res.Status = StatusError
		res.Err = err
		return res
	}

	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(domain), qtype)
	msg.RecursionDesired = true

	client := &dns.Client{Net: "udp", Timeout: timeout, UDPSize: 4096}

	var (
		reply *dns.Msg
		rtt   time.Duration
		last  error
	)
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			last = ctx.Err()
			break
		}
		reply, rtt, last = client.ExchangeContext(ctx, msg, srv.Addr())
		if last == nil && reply != nil && reply.Truncated {
			tcp := &dns.Client{Net: "tcp", Timeout: timeout}
			if r2, rtt2, err2 := tcp.ExchangeContext(ctx, msg, srv.Addr()); err2 == nil {
				reply, rtt = r2, rtt2
			}
		}
		if last == nil {
			break
		}
	}

	res.RTT = rtt
	if last != nil {
		res.Err = last
		var nerr net.Error
		switch {
		case errors.As(last, &nerr) && nerr.Timeout():
			res.Status = StatusTimeout
		case errors.Is(last, context.DeadlineExceeded), errors.Is(last, context.Canceled):
			res.Status = StatusTimeout
		default:
			res.Status = StatusError
		}
		return res
	}

	switch reply.Rcode {
	case dns.RcodeSuccess:
	case dns.RcodeNameError:
		res.Status = StatusNXDomain
		return res
	case dns.RcodeRefused:
		res.Status = StatusRefused
		return res
	case dns.RcodeServerFailure:
		res.Status = StatusServFail
		return res
	default:
		res.Status = StatusError
		res.Err = fmt.Errorf("rcode %s", dns.RcodeToString[reply.Rcode])
		return res
	}

	res.Answers, res.Records = answerSet(reply.Answer, domain, qtype, preferAddresses)
	if len(res.Answers) == 0 {
		res.Status = StatusEmpty
		return res
	}
	res.Status = StatusOK
	return res
}

func canon(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

func answerSet(rrs []dns.RR, qname string, qtype uint16, preferAddresses bool) (answers, records []string) {
	var cnames []*dns.CNAME
	var direct []dns.RR
	for _, rr := range rrs {
		records = append(records, dns.TypeToString[rr.Header().Rrtype]+" "+format(rr))
		if c, ok := rr.(*dns.CNAME); ok && qtype != dns.TypeCNAME {
			cnames = append(cnames, c)
		}
		if rr.Header().Rrtype == qtype {
			direct = append(direct, rr)
		}
	}

	if len(cnames) > 0 && !preferAddresses {
		return []string{"CNAME " + terminalTarget(qname, cnames)}, records
	}

	out := make([]string, 0, len(direct))
	for _, rr := range direct {
		out = append(out, format(rr))
	}
	sort.Strings(out)
	return dedupe(out), records
}

func terminalTarget(qname string, cnames []*dns.CNAME) string {
	targets := make(map[string]string, len(cnames))
	for _, c := range cnames {
		targets[canon(c.Hdr.Name)] = canon(c.Target)
	}

	start := canon(qname)
	name := start
	seen := map[string]bool{}
	for !seen[name] {
		seen[name] = true
		next, ok := targets[name]
		if !ok {
			break
		}
		name = next
	}
	if name != start {
		return name
	}

	for _, c := range cnames {
		if _, isOwner := targets[canon(c.Target)]; !isOwner {
			return canon(c.Target)
		}
	}
	return canon(cnames[len(cnames)-1].Target)
}

func format(rr dns.RR) string {
	switch v := rr.(type) {
	case *dns.A:
		return v.A.String()
	case *dns.AAAA:
		return v.AAAA.String()
	case *dns.CNAME:
		return strings.TrimSuffix(v.Target, ".")
	case *dns.NS:
		return strings.TrimSuffix(v.Ns, ".")
	case *dns.PTR:
		return strings.TrimSuffix(v.Ptr, ".")
	case *dns.MX:
		return fmt.Sprintf("%d %s", v.Preference, strings.TrimSuffix(v.Mx, "."))
	case *dns.TXT:
		return strings.Join(v.Txt, "")
	case *dns.SOA:
		return fmt.Sprintf("%s %s %d", strings.TrimSuffix(v.Ns, "."), strings.TrimSuffix(v.Mbox, "."), v.Serial)
	case *dns.CAA:
		return fmt.Sprintf("%d %s %s", v.Flag, v.Tag, v.Value)
	case *dns.SRV:
		return fmt.Sprintf("%d %d %d %s", v.Priority, v.Weight, v.Port, strings.TrimSuffix(v.Target, "."))
	default:
		fields := strings.Fields(rr.String())
		if len(fields) > 4 {
			return strings.Join(fields[4:], " ")
		}
		return rr.String()
	}
}

func dedupe(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
