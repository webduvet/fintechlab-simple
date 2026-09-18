// Package allowlist enforces webhook destination policy.
// This is a local lab control (compose hostnames, loopback). It is not
// a public-CIDR production allowlist.
package allowlist

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// List matches URL hosts against exact hostnames, host:port pairs, and CIDRs.
type List struct {
	exact map[string]struct{}
	cidrs []*net.IPNet
}

// Parse comma/space-separated entries. Empty input is a deny-all list.
func Parse(spec string) (*List, error) {
	l := &List{exact: map[string]struct{}{}}
	for _, raw := range strings.FieldsFunc(spec, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	}) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			_, n, err := net.ParseCIDR(raw)
			if err != nil {
				return nil, fmt.Errorf("allowlist: cidr %q: %w", raw, err)
			}
			l.cidrs = append(l.cidrs, n)
			continue
		}
		l.exact[strings.ToLower(raw)] = struct{}{}
	}
	return l, nil
}

// Allowed reports whether dest may be used as a webhook URL.
func (l *List) Allowed(dest string) error {
	if l == nil || (len(l.exact) == 0 && len(l.cidrs) == 0) {
		return fmt.Errorf("allowlist: empty list denies all destinations")
	}
	u, err := url.Parse(dest)
	if err != nil {
		return fmt.Errorf("allowlist: parse: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("allowlist: scheme %q rejected (http/https only)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("allowlist: missing host")
	}
	if u.User != nil {
		return fmt.Errorf("allowlist: userinfo not allowed")
	}
	hostport := strings.ToLower(u.Host)
	host := strings.ToLower(u.Hostname())
	if _, ok := l.exact[hostport]; ok {
		return nil
	}
	if _, ok := l.exact[host]; ok {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, n := range l.cidrs {
			if n.Contains(ip) {
				return nil
			}
		}
	}
	return fmt.Errorf("allowlist: destination host %q is not permitted", u.Host)
}
