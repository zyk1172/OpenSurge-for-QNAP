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

func TestDNSPacketProbeUDPAndTCP(t *testing.T) {
    for _, network := range []string{"udp", "tcp"} {
        t.Run(network, func(t *testing.T) {
            name, err := dnsmessage.NewName("opensurge-health-check.lan.")
            if err != nil { t.Fatal(err) }
            var address string
            done := make(chan error, 1)
            if network == "udp" {
                listener, err := net.ListenPacket("udp", "127.0.0.1:0")
                if err != nil { t.Fatal(err) }
                defer listener.Close()
                address = listener.LocalAddr().String()
                go func() {
                    buf := make([]byte, 4096)
                    n, peer, err := listener.ReadFrom(buf)
                    if err != nil { done <- err; return }
                    var msg dnsmessage.Message
                    if err := msg.Unpack(buf[:n]); err != nil { done <- err; return }
                    msg.Header.Response = true
                    reply, err := msg.Pack()
                    if err == nil { _,err = listener.WriteTo(reply,peer) }
                    done <- err
                }()
            } else {
                listener, err := net.Listen("tcp", "127.0.0.1:0")
                if err != nil { t.Fatal(err) }
                defer listener.Close()
                address=listener.Addr().String()
                go func() {
                    conn,err := listener.Accept()
                    if err != nil { done<-err;return }
                    defer conn.Close()
                    prefix:=make([]byte,2)
                    if _,err:=io.ReadFull(conn,prefix);err!=nil {done<-err;return}
                    b:=make([]byte,int(binary.BigEndian.Uint16(prefix)))
                    if _,err:=io.ReadFull(conn,b);err!=nil {done<-err;return}
                    var msg dnsmessage.Message
                    if err:=msg.Unpack(b);err!=nil {done<-err;return}
                    msg.Header.Response=true
                    reply,err:=msg.Pack()
                    if err==nil { out:=make([]byte,2+len(reply));binary.BigEndian.PutUint16(out[:2],uint16(len(reply)));copy(out[2:],reply);_,err=conn.Write(out) }
                    done<-err
                }()
            }
            rcode,err:=queryDNSResponse(t.Context(),network,address,name)
            if err!=nil || rcode!=dnsmessage.RCodeSuccess {t.Fatalf("DNS probe %s rcode=%v err=%v",network,rcode,err)}
            if err:=<-done;err!=nil {t.Fatal(err)}
        })
    }
}
