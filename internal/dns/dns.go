// Package dns answers every name under Dotlane's domain endings with this machine.
//
// It replaces the prototype's dnsmasq rule (address=/traix/127.0.0.1 and ::1). The OS resolver
// sends only those endings here, so anything else is refused rather than forwarded.
package dns

import (
	"net"
	"strings"

	"github.com/miekg/dns"
)

var (
	loopback4 = net.IPv4(127, 0, 0, 1)
	loopback6 = net.IPv6loopback
)

// Server is a running responder, on UDP and TCP at the same address.
type Server struct {
	udp, tcp *dns.Server
	addr     string
}

// Start binds addr (such as 127.0.0.1:10053) before returning, so a busy port is reported here,
// then serves in the background.
func Start(addr string, tlds []string) (*Server, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	// Bind TCP to the port UDP got, which matters when addr asks for port 0.
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		pc.Close()
		return nil, err
	}
	h := handler(tlds)
	s := &Server{
		udp:  &dns.Server{PacketConn: pc, Handler: h},
		tcp:  &dns.Server{Listener: ln, Handler: h},
		addr: pc.LocalAddr().String(),
	}
	go s.udp.ActivateAndServe()
	go s.tcp.ActivateAndServe()
	return s, nil
}

// Addr is the address actually bound.
func (s *Server) Addr() string { return s.addr }

// Shutdown stops both listeners.
func (s *Server) Shutdown() {
	s.udp.Shutdown()
	s.tcp.Shutdown()
}

func handler(tlds []string) dns.HandlerFunc {
	zones := make([]string, len(tlds))
	for i, tld := range tlds {
		zones[i] = dns.Fqdn(tld)
	}
	inZone := func(name string) bool {
		for _, z := range zones {
			if dns.IsSubDomain(z, name) {
				return true
			}
		}
		return false
	}

	return func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		for _, q := range req.Question {
			name := strings.ToLower(q.Name)
			if q.Qclass != dns.ClassINET || !inZone(name) {
				m.Rcode = dns.RcodeRefused
				m.Answer = nil
				break
			}
			m.Authoritative = true
			// TTL 0: the answer never changes, but caching it buys nothing on loopback and a
			// zero TTL keeps a changed ending from lingering in the OS cache.
			hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: 0}
			switch q.Qtype {
			case dns.TypeA:
				hdr.Rrtype = dns.TypeA
				m.Answer = append(m.Answer, &dns.A{Hdr: hdr, A: loopback4})
			case dns.TypeAAAA:
				hdr.Rrtype = dns.TypeAAAA
				m.Answer = append(m.Answer, &dns.AAAA{Hdr: hdr, AAAA: loopback6})
			}
			// Other types: the name exists but has no such record (NOERROR, empty answer).
		}
		w.WriteMsg(m)
	}
}
