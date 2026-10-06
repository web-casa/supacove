package netguard

import (
	"net"
	"testing"
)

func TestCheck(t *testing.T) {
	allowed := []string{
		"93.184.216.34",                      // public v4
		"2606:2800:220:1:248:1893:25c8:1946", // public v6
		"127.0.0.1",                          // loopback: LAN/self receivers are legal
		"192.168.1.10",                       // private v4: LAN receivers are legal
		"fd00::5",                            // private v6 (NOT the AWS metadata address)
	}
	for _, s := range allowed {
		if err := Check(net.ParseIP(s)); err != nil {
			t.Errorf("Check(%s) refused a legal target: %v", s, err)
		}
	}

	refused := []string{
		"169.254.169.254",        // IMDSv4
		"::ffff:169.254.169.254", // v4-mapped IMDSv4
		"fe80::1",                // link-local unicast
		"ff02::1",                // link-local multicast
		"fd00:ec2::254",          // AWS IMDSv6
		"64:ff9b::a9fe:a9fe",     // NAT64/WKP encoding of IMDSv4
		"64:ff9b:1::a9fe:a9fe",   // NAT64 local encoding of IMDSv4
	}
	for _, s := range refused {
		if err := Check(net.ParseIP(s)); err == nil {
			t.Errorf("Check(%s) allowed a forbidden target", s)
		}
	}
}
