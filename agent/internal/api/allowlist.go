package api

import (
	"encoding/json"
	"log"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// ipAllowlist restricts every request to source addresses listed in
// `allowed_ips`. The agent has no request authentication, so this is the
// primary network-level gate; it deliberately only trusts the TCP peer address
// and ignores X-Forwarded-For (which any client could forge).
type ipAllowlist struct {
	prefixes []netip.Prefix

	mu       sync.Mutex
	lastDeny map[string]time.Time
}

// denyLogInterval keeps a misconfigured or hostile client from flooding the
// journal: one line per source IP per interval.
const denyLogInterval = time.Minute

func newIPAllowlist(specs []string) (*ipAllowlist, error) {
	a := &ipAllowlist{lastDeny: make(map[string]time.Time)}
	for _, raw := range specs {
		var p netip.Prefix
		var err error
		if containsSlash(raw) {
			p, err = netip.ParsePrefix(raw)
			if err == nil {
				p = p.Masked()
			}
		} else {
			var addr netip.Addr
			addr, err = netip.ParseAddr(raw)
			if err == nil {
				addr = addr.Unmap()
				p = netip.PrefixFrom(addr, addr.BitLen())
			}
		}
		if err != nil {
			return nil, err
		}
		a.prefixes = append(a.prefixes, p)
	}
	return a, nil
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}

func (a *ipAllowlist) allows(remote string) bool {
	ap, err := netip.ParseAddrPort(remote)
	if err != nil {
		return false
	}
	addr := ap.Addr().Unmap()
	for _, p := range a.prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func (a *ipAllowlist) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.allows(r.RemoteAddr) {
			a.logDenied(r.RemoteAddr)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "forbidden: source ip is not in allowed_ips",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *ipAllowlist) logDenied(remote string) {
	host := remote
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		host = ap.Addr().String()
	}
	now := time.Now()
	a.mu.Lock()
	if last, ok := a.lastDeny[host]; ok && now.Sub(last) < denyLogInterval {
		a.mu.Unlock()
		return
	}
	a.lastDeny[host] = now
	a.mu.Unlock()
	log.Printf("[agent] rejected request from %s (not in allowed_ips)", host)
}
