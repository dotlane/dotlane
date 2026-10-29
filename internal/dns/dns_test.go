package dns

import (
	"testing"

	"github.com/miekg/dns"
)

func query(t *testing.T, addr, name string, qtype uint16, network string) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	c := &dns.Client{Net: network}
	r, _, err := c.Exchange(m, addr)
	if err != nil {
		t.Fatalf("%s %s: %v", name, dns.TypeToString[qtype], err)
	}
	return r
}

func TestResponder(t *testing.T) {
	s, err := Start("127.0.0.1:0", []string{"traix", "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()

	for _, network := range []string{"udp", "tcp"} {
		r := query(t, s.Addr(), "myapp.traix", dns.TypeA, network)
		if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 1 || r.Answer[0].(*dns.A).A.String() != "127.0.0.1" {
			t.Errorf("%s A: %v", network, r)
		}
	}

	r := query(t, s.Addr(), "Deep.Name.TEST", dns.TypeAAAA, "udp")
	if len(r.Answer) != 1 || r.Answer[0].(*dns.AAAA).AAAA.String() != "::1" || !r.Authoritative {
		t.Errorf("AAAA, mixed case, second ending: %v", r)
	}

	r = query(t, s.Addr(), "x.traix", dns.TypeMX, "udp")
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Errorf("MX: want NOERROR with no answer, got %v", r)
	}

	for _, name := range []string{"example.com", "traix.com", "nottraix"} {
		r = query(t, s.Addr(), name, dns.TypeA, "udp")
		if r.Rcode != dns.RcodeRefused || len(r.Answer) != 0 {
			t.Errorf("%s: want REFUSED, got %v", name, r)
		}
	}
}

func TestStartReportsBusyPort(t *testing.T) {
	s, err := Start("127.0.0.1:0", []string{"traix"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	if _, err := Start(s.Addr(), []string{"traix"}); err == nil {
		t.Error("second Start on the same port succeeded")
	}
}
