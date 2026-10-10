package controlapi

import (
    "reflect"
    "testing"
)

func TestParseDNSRuntimeUsesAppliedSmartDNSConfiguration(t *testing.T) {
    conf:=[]byte("bind 192.168.2.241:53 -group opensurge-resolver\n"+
        "bind-tcp 192.168.2.241:53 -group opensurge-resolver\n"+
        "server 127.0.0.1:1053 -group opensurge-gateway -exclude-default-group\n"+
        "server 192.168.2.1 -group opensurge-resolver -exclude-default-group\n"+
        "server 223.5.5.5 -group opensurge-resolver -exclude-default-group\n"+
        "server 127.0.0.1:5353 -group opensurge-local -exclude-default-group\n")
    got:=parseDNSRuntime(conf)
    if !got.Configured || got.Listen!="192.168.2.241:53" || got.DefaultView!="resolver" ||
        got.GatewayUpstream!="127.0.0.1:1053" ||
        !reflect.DeepEqual(got.ResolverUpstreams,[]string{"192.168.2.1","223.5.5.5"}) {
        t.Fatalf("applied SmartDNS view parsed incorrectly: %#v",got)
    }
}
