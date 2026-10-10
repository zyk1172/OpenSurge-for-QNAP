package controlapi

import (
    "context"
    "encoding/binary"
    "fmt"
    "io"
    "net"
    "strings"
    "sync"
    "time"

    "golang.org/x/net/dns/dnsmessage"
    "open-mihomo-gateway/internal/config"
    "open-mihomo-gateway/internal/dhcp"
)

type dnsProbeResult uint8

const (
    dnsProbeHealthy dnsProbeResult = iota
    dnsProbeUnknown
    dnsProbeUnavailable
    dnsProbeInterval = 30 * time.Second
    dnsProbeTimeout = 800 * time.Millisecond
    dnsProbeFailuresRequired = 2
)

// A low-frequency in-process socket probe. Never spawn ip/nft/dig processes:
// stalled NAS storage can leave those commands in uninterruptible I/O sleep.
type dnsHealthProbe struct {
    mu sync.Mutex
    next time.Time
    inFlight bool
    failures int
    result dnsProbeResult
    check func(context.Context, config.Config) dnsProbeResult
}

func newDNSHealthProbe() *dnsHealthProbe {
    return &dnsHealthProbe{result: dnsProbeUnknown, check: checkDNSDependencies}
}

func (p *dnsHealthProbe) reset() {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.failures = 0
    p.next = time.Time{}
    p.result = dnsProbeUnknown
}

func (p *dnsHealthProbe) sample(ctx context.Context, cfg config.Config) dnsProbeResult {
    p.mu.Lock()
    if p.inFlight {
        p.mu.Unlock()
        return dnsProbeUnknown
    }
    if time.Now().Before(p.next) {
        result := p.result
        p.mu.Unlock()
        return result
    }
    p.inFlight = true
    p.next = time.Now().Add(dnsProbeInterval)
    p.mu.Unlock()

    check := p.check
    if check == nil { check = checkDNSDependencies }
    result := check(ctx, cfg)
    p.mu.Lock()
    defer p.mu.Unlock()
    p.inFlight = false
    switch result {
    case dnsProbeHealthy:
        p.failures = 0
        p.result = dnsProbeHealthy
    case dnsProbeUnavailable:
        p.failures++
        if p.failures >= dnsProbeFailuresRequired {
            p.result = dnsProbeUnavailable
        } else {
            p.result = dnsProbeUnknown
        }
    default:
        // SERVFAIL/REFUSED means a DNS packet was received: likely an upstream
        // problem, not proof that restarting the gateway can help.
        p.failures = 0
        p.result = dnsProbeUnknown
    }
    return p.result
}

func checkDNSDependencies(ctx context.Context, cfg config.Config) dnsProbeResult {
    lan := strings.TrimSpace(cfg.DNS.Listen)
    if lan == "" { lan = strings.TrimSpace(cfg.Gateway.LANIP) }
    if net.ParseIP(lan) == nil || cfg.DNS.Port <= 0 || cfg.DNS.Port > 65535 {
        return dnsProbeUnknown
    }
    name := "example.com."
    if domain := strings.Trim(strings.TrimSpace(cfg.DHCP.Domain), "."); domain != "" {
        // The local domain is handled by dnsmasq and does not need a public
        // resolver or external traffic to answer.
        name = "opensurge-health-check." + domain + "."
    }
    question, err := dnsmessage.NewName(name)
    if err != nil { return dnsProbeUnknown }
    targets := []string{net.JoinHostPort(lan, fmt.Sprint(cfg.DNS.Port))}
    if dhcp.ShouldRun(cfg) {
        targets = append(targets, "127.0.0.1:5353")
    }
    result := dnsProbeHealthy
    for _, target := range targets {
        for _, network := range []string{"udp", "tcp"} {
            rcode, err := queryDNSResponse(ctx, network, target, question)
            if err != nil { return dnsProbeUnavailable }
            if rcode == dnsmessage.RCodeServerFailure || rcode == dnsmessage.RCodeRefused {
                result = dnsProbeUnknown
            }
        }
    }
    return result
}

func queryDNSResponse(ctx context.Context, network, address string, name dnsmessage.Name) (dnsmessage.RCode, error) {
    msg := dnsmessage.Message{
        Header: dnsmessage.Header{ID: 0xA513, RecursionDesired: true},
        Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
    }
    request, err := msg.Pack()
    if err != nil { return 0, err }
    probeCtx, cancel := context.WithTimeout(ctx, dnsProbeTimeout)
    defer cancel()
    conn, err := (&net.Dialer{}).DialContext(probeCtx, network, address)
    if err != nil { return 0, err }
    defer conn.Close()
    if err := conn.SetDeadline(time.Now().Add(dnsProbeTimeout)); err != nil { return 0, err }
    if network == "tcp" {
        prefix := []byte{byte(len(request) >> 8), byte(len(request))}
        if _, err := conn.Write(append(prefix, request...)); err != nil { return 0, err }
    } else if _, err := conn.Write(request); err != nil {
        return 0, err
    }
    response := make([]byte, 4096)
    var size int
    if network == "tcp" {
        prefix := make([]byte, 2)
        if _, err := io.ReadFull(conn, prefix); err != nil { return 0, err }
        size = int(binary.BigEndian.Uint16(prefix))
        if size < 12 || size > len(response) { return 0, fmt.Errorf("invalid DNS reply length %d", size) }
        if _, err := io.ReadFull(conn, response[:size]); err != nil { return 0, err }
    } else {
        size, err = conn.Read(response)
        if err != nil { return 0, err }
    }
    var reply dnsmessage.Message
    if err := reply.Unpack(response[:size]); err != nil { return 0, err }
    if !reply.Header.Response || reply.Header.ID != msg.Header.ID {
        return 0, fmt.Errorf("unexpected DNS response header")
    }
    return reply.Header.RCode, nil
}
