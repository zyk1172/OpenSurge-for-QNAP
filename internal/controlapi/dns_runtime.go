package controlapi

import (
    "errors"
    "net/http"
    "os"
    "strings"

    "open-mihomo-gateway/internal/config"
    "open-mihomo-gateway/internal/runtime"
)

// DNSRuntime describes the SmartDNS config actually written for the running
// gateway. It is not derived from Docker's current resolv.conf and does not
// claim that its upstream servers are reachable.
type DNSRuntime struct {
    Configured bool `json:"configured"`
    Listen string `json:"listen,omitempty"`
    DefaultView string `json:"default_view,omitempty"`
    GatewayUpstream string `json:"gateway_upstream,omitempty"`
    ResolverUpstreams []string `json:"resolver_upstreams"`
}

func parseDNSRuntime(data []byte) DNSRuntime {
    snapshot:=DNSRuntime{Configured:true, ResolverUpstreams:[]string{}}
    for _,line:=range strings.Split(string(data),"
"){
        parts:=strings.Fields(strings.TrimSpace(line))
        if len(parts)<2 || strings.HasPrefix(parts[0],"#"){continue}
        switch parts[0]{
        case "bind":
            snapshot.Listen=parts[1]
            if containsGroup(parts,"opensurge-resolver"){snapshot.DefaultView="resolver"}
            if containsGroup(parts,"opensurge-gateway"){snapshot.DefaultView="gateway"}
        case "server":
            if containsGroup(parts,"opensurge-gateway"){
                snapshot.GatewayUpstream=parts[1]
            } else if containsGroup(parts,"opensurge-resolver"){
                snapshot.ResolverUpstreams=append(snapshot.ResolverUpstreams,parts[1])
            }
        }
    }
    return snapshot
}

func containsGroup(fields []string, group string) bool {
    for i:=0;i+1<len(fields);i++{
        if fields[i]=="-group" && fields[i+1]==group{return true}
    }
    return false
}

func (s *Server) handleDNSRuntime(w http.ResponseWriter, r *http.Request) {
    cfg,err:=config.Load(s.configPath)
    if err!=nil{
        writeError(w,http.StatusInternalServerError,"dns_runtime_invalid",err.Error())
        return
    }
    conf:=runtime.NewPaths(cfg).SmartDNSConf
    data,err:=os.ReadFile(conf)
    if errors.Is(err,os.ErrNotExist){
        writeJSON(w,http.StatusOK,DNSRuntime{ResolverUpstreams:[]string{}})
        return
    }
    if err!=nil{
        writeError(w,http.StatusInternalServerError,"dns_runtime_unavailable",err.Error())
        return
    }
    writeJSON(w,http.StatusOK,parseDNSRuntime(data))
}
