package config

import (
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// hostIPv6ProbeTTL bounds how long a real-connectivity probe is trusted before
// it is taken again. Both probes below are cheap (a syscall or a small file
// read), but the mtproto package calls them on every dial, so caching keeps a
// busy proxy from repeating the same read hundreds of times a second.
const hostIPv6ProbeTTL = 30 * time.Second

const ipv6RouteFile = "/proc/net/ipv6_route"

var (
	ipv6Now       = time.Now
	hostIPv6Probe = probeGlobalIPv6

	hostIPv6Mu      sync.Mutex
	hostIPv6Known   bool
	hostIPv6Present bool
	hostIPv6At      time.Time

	hostIPv6RouteProbe = probeIPv6DefaultRoute

	hostIPv6RouteMu      sync.Mutex
	hostIPv6RouteKnown   bool
	hostIPv6RoutePresent bool
	hostIPv6RouteAt      time.Time
)

func isGlobalRoutableIPv6(ip net.IP) bool {
	if ip == nil || ip.To4() != nil || !ip.IsGlobalUnicast() {
		return false
	}
	v6 := ip.To16()
	return v6 != nil && v6[0]&0xfe != 0xfc
}

func probeGlobalIPv6() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok || ipNet.IP == nil {
				continue
			}
			if !isGlobalRoutableIPv6(ipNet.IP) {
				continue
			}
			return true
		}
	}
	return false
}

// HostHasGlobalIPv6 reports whether this host has a globally routable IPv6
// address on any up, non-loopback interface.
func HostHasGlobalIPv6() bool {
	hostIPv6Mu.Lock()
	defer hostIPv6Mu.Unlock()

	now := ipv6Now()
	if hostIPv6Known && now.Sub(hostIPv6At) < hostIPv6ProbeTTL {
		return hostIPv6Present
	}
	hostIPv6Present = hostIPv6Probe()
	hostIPv6Known = true
	hostIPv6At = now
	return hostIPv6Present
}

// HostHasIPv6DefaultRoute reports whether the kernel has a usable IPv6 default
// route, i.e. an address alone is not enough: a host can carry a global address
// with no route out (a stale SLAAC prefix after the router stopped announcing
// it), which fails every dial exactly like having no IPv6 at all.
func HostHasIPv6DefaultRoute() bool {
	hostIPv6RouteMu.Lock()
	defer hostIPv6RouteMu.Unlock()

	now := ipv6Now()
	if hostIPv6RouteKnown && now.Sub(hostIPv6RouteAt) < hostIPv6ProbeTTL {
		return hostIPv6RoutePresent
	}
	hostIPv6RoutePresent = hostIPv6RouteProbe()
	hostIPv6RouteKnown = true
	hostIPv6RouteAt = now
	return hostIPv6RoutePresent
}

// HostCanReachIPv6 reports whether this host can actually dial out over IPv6,
// as opposed to merely being configured to consider AAAA records. A router or
// server can carry an IPv6 address with no usable route (or vice versa), and
// dialing an address that will never connect burns the same few seconds of
// budget Telegram gives a session before calling the proxy misconfigured.
func HostCanReachIPv6() bool {
	return HostHasGlobalIPv6() && HostHasIPv6DefaultRoute()
}

func probeIPv6DefaultRoute() bool {
	data, err := os.ReadFile(ipv6RouteFile)
	if err != nil {
		return false
	}
	return parseIPv6DefaultRoute(string(data))
}

const (
	rtfUp      = 0x0001
	rtfReject  = 0x0200
	ipv6AnyHex = "00000000000000000000000000000000"
)

func parseIPv6DefaultRoute(data string) bool {
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		if fields[0] != ipv6AnyHex || fields[1] != "00" {
			continue
		}
		if fields[9] == "lo" {
			continue
		}
		flags, err := strconv.ParseUint(fields[8], 16, 32)
		if err != nil {
			continue
		}
		if flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		return true
	}
	return false
}
