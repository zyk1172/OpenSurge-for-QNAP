package controlapi

import (
    "context"
    "encoding/binary"
    "io"
    "net"
    "testing"
    "golang.org/x/net/dns/dnsmessage"
    "time"

    "open-mihomo-gateway/internal/config"
)

func TestDNSHealthProbeConsecutiveFailuresAndCooldown(t *testing.T) {
    p := newDNSHealthProbe()
    calls := 0
    p.check = func(context.Context, config.Config) dnsProbeResult { calls++; return dnsProbeUnavailable }
    if got:=p.sample(t.Context(),config.Config{}); got!=dnsProbeUnknown { t.Fatalf("first failure=%v",got) }
    if got:=p.sample(t.Context(),config.Config{}); got!=dnsProbeUnknown || calls!=1 { t.Fatalf("probe not rate-limited: result=%v calls=%d",got,calls) }
    p.mu.Lock(); p.next=time.Now().Add(-time.Second); p.mu.Unlock()
    if got:=p.sample(t.Context(),config.Config{}); got!=dnsProbeUnavailable { t.Fatalf("second failure=%v",got) }
    if calls!=2 { t.Fatalf("probe count=%d",calls) }
    p.reset()
    if got:=p.sample(t.Context(),config.Config{}); got!=dnsProbeUnknown { t.Fatalf("old failure survived recovery=%v",got) }
}

func TestDNSHealthProbeDoesNotRestartOnUpstreamFailure(t *testing.T) {
    p:=newDNSHealthProbe()
    p.check=func(context.Context,config.Config) dnsProbeResult{return dnsProbeUnknown}
    for i:=0;i<5;i++ {
        p.mu.Lock();p.next=time.Time{};p.mu.Unlock()
        if got:=p.sample(t.Context(),config.Config{});got!=dnsProbeUnknown { t.Fatalf("upstream failure would trigger restart: %v",got) }
    }
    if p.failures!=0 { t.Fatalf("upstream response accumulated failures: %d",p.failures) }
}
