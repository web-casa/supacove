// Package netguard centralizes the SSRF dial boundary for every outbound
// request supabackup makes on behalf of configuration (webhooks, heartbeat).
// Link-local targets are always refused; the IPv6 cloud-metadata endpoint
// (fd00:ec2::254) and NAT64/WKP encodings of link-local addresses are
// refused too. Private and loopback ranges stay allowed on purpose: LAN
// self-hosted receivers are a supported configuration.
package netguard

import (
	"fmt"
	"net"
)

// awsIMDSv6 is the EC2 IPv6 metadata endpoint.
var awsIMDSv6 = net.ParseIP("fd00:ec2::254")

// NAT64 prefixes that embed an IPv4 address in the low 32 bits.
var nat64Prefixes = mustPrefixes("64:ff9b::/96", "64:ff9b:1::/48")

// Check refuses addr when it is a link-local address, the AWS IPv6 metadata
// endpoint, or a NAT64 encoding of a link-local address.
func Check(ip net.IP) error {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("link-local destination %s is not allowed", ip)
	}
	if awsIMDSv6.Equal(ip) {
		return fmt.Errorf("cloud metadata endpoint %s is not allowed", ip)
	}
	if v4 := embeddedIPv4(ip); v4 != nil && (v4.IsLinkLocalUnicast() || v4.IsLinkLocalMulticast()) {
		return fmt.Errorf("NAT64-encoded link-local destination %s is not allowed", ip)
	}
	return nil
}

// embeddedIPv4 extracts the IPv4 address carried by a NAT64 address; nil for
// plain IPv4 and for IPv6 addresses outside the NAT64 prefixes.
func embeddedIPv4(ip net.IP) net.IP {
	if ip.To4() != nil {
		return nil
	}
	v6 := ip.To16()
	if v6 == nil {
		return nil
	}
	for _, p := range nat64Prefixes {
		if p.Contains(ip) {
			return net.IP(v6[12:16])
		}
	}
	return nil
}

func mustPrefixes(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, s := range cidrs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic("netguard: bad prefix " + s)
		}
		out = append(out, n)
	}
	return out
}
