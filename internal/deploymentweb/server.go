package deploymentweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"open-mihomo-gateway/internal/deployment"
)

type Server struct {
	addr string
	token string
	orchestrator *deployment.RPCClient
}

type Options struct {
	Addr string
	Token string
	OrchestratorSocket string
}

type hostInterface struct {
	Name string `json:"name"`
	IPv4 string `json:"ipv4,omitempty"`
	CIDR string `json:"cidr,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	Default bool `json:"default"`
}

func New(options Options) (*Server, error) {
	if strings.TrimSpace(options.Addr) == "" {
		options.Addr = "127.0.0.1:61779"
	}
	if strings.TrimSpace(options.Token) == "" {
		return nil, fmt.Errorf("deployment control token is required")
	}
	if strings.TrimSpace(options.OrchestratorSocket) == "" {
		options.OrchestratorSocket = "/run/opensurge/orchestrator.sock"
	}
	return &Server{
		addr: options.Addr,
		token: options.Token,
		orchestrator: deployment.NewRPCClient(options.OrchestratorSocket),
	}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	httpServer := &http.Server{
		Addr: s.addr,
		Handler: s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 95 * time.Second,
		WriteTimeout: 95 * time.Second,
		IdleTimeout: 60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	err := httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handlePage)
	mux.HandleFunc("GET /api/v1/deployment/interfaces", s.requireToken(s.handleInterfaces))
	mux.HandleFunc("GET /api/v1/deployment", s.requireToken(s.handleStatus))
	mux.HandleFunc("POST /api/v1/deployment", s.requireToken(s.handleApply))
	return mux
}

func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid internal control token"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handlePage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
	_, _ = w.Write([]byte(managerHTML))
}

func (s *Server) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	interfaces, err := discoverHostInterfaces(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": interfaces})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.orchestrator.Status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var spec deployment.Spec
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid deployment request"})
		return
	}
	spec.Normalize()
	if err := spec.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	status, err := s.orchestrator.Apply(r.Context(), spec)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type ipAddress struct {
	IfName string `json:"ifname"`
	Flags []string `json:"flags"`
	AddrInfo []struct {
		Family string `json:"family"`
		Local string `json:"local"`
		PrefixLen int `json:"prefixlen"`
		Scope string `json:"scope"`
	} `json:"addr_info"`
}

type ipRoute struct {
	Gateway string `json:"gateway"`
	Dev string `json:"dev"`
}

func discoverHostInterfaces(ctx context.Context) ([]hostInterface, error) {
	addrOut, err := run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return nil, err
	}
	routeOut, err := run(ctx, "ip", "-j", "route", "show", "default")
	if err != nil {
		return nil, err
	}
	var addresses []ipAddress
	if err := json.Unmarshal(addrOut, &addresses); err != nil {
		return nil, fmt.Errorf("decode host interfaces: %w", err)
	}
	var routes []ipRoute
	if err := json.Unmarshal(routeOut, &routes); err != nil {
		return nil, fmt.Errorf("decode host routes: %w", err)
	}
	gatewayByDev := map[string]string{}
	for _, route := range routes {
		if route.Dev != "" && net.ParseIP(route.Gateway).To4() != nil {
			gatewayByDev[route.Dev] = route.Gateway
		}
	}
	result := make([]hostInterface, 0, len(addresses))
	for _, entry := range addresses {
		if entry.IfName == "lo" || !contains(entry.Flags, "UP") {
			continue
		}
		item := hostInterface{Name: entry.IfName, Gateway: gatewayByDev[entry.IfName]}
		item.Default = item.Gateway != ""
		for _, addr := range entry.AddrInfo {
			if addr.Family == "inet" && net.ParseIP(addr.Local).To4() != nil && addr.PrefixLen >= 0 && addr.PrefixLen <= 32 {
				item.IPv4 = addr.Local
				item.CIDR = fmt.Sprintf("%s/%d", networkAddress(addr.Local, addr.PrefixLen), addr.PrefixLen)
				break
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func networkAddress(ipText string, prefix int) string {
	ip := net.ParseIP(ipText).To4()
	if ip == nil {
		return ipText
	}
	mask := net.CIDRMask(prefix, 32)
	return ip.Mask(mask).String()
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	out, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const managerHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenSurge for QNAP 部署</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#f5f5f7;color:#1d1d1f;margin:0}.wrap{max-width:760px;margin:40px auto;padding:24px}.card{background:white;border-radius:18px;padding:24px;box-shadow:0 8px 30px #0000000f}.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}label{font-size:13px;color:#666;display:block;margin-bottom:6px}input,select{box-sizing:border-box;width:100%;padding:11px 12px;border:1px solid #d2d2d7;border-radius:10px;background:white;font-size:15px}button{border:0;border-radius:10px;background:#0071e3;color:white;padding:11px 18px;font-size:15px;cursor:pointer}button:disabled{opacity:.5}.full{grid-column:1/-1}.hint{color:#6e6e73;font-size:13px;line-height:1.5}.status{margin-top:18px;padding:12px;border-radius:10px;background:#f5f5f7;white-space:pre-wrap}.error{color:#b42318}h1{font-size:28px;margin-top:0}@media(max-width:640px){.grid{grid-template-columns:1fr}.wrap{margin:0;padding:16px}}
</style></head><body><div class="wrap"><div class="card"><h1>OpenSurge for QNAP</h1>
<p class="hint">这里配置 QNAP 网卡、OpenSurge 独立 IP、主路由和持久化目录。无需编辑 .env。部署完成后代理、订阅、规则和设备策略继续在 Gateway Web 中配置。</p>
<div class="grid">
<div class="full"><label>QNAP 父网卡 / Virtual Switch</label><select id="iface"></select></div>
<div><label>OpenSurge 独立 IPv4</label><input id="ip" placeholder="192.168.2.241"></div>
<div><label>LAN CIDR</label><input id="subnet" placeholder="192.168.2.0/24"></div>
<div><label>主路由 IPv4</label><input id="gateway" placeholder="192.168.2.1"></div>
<div><label>QNAP 持久化目录</label><input id="data" value="/share/Container/opensurge"></div>
<div class="full"><button id="apply">应用并创建/重建 Gateway</button></div>
</div><div id="status" class="status">正在读取部署状态…</div></div></div>
<script>
const q=id=>document.getElementById(id), status=q('status');
async function api(path,options){const r=await fetch(path,options);const j=await r.json();if(!r.ok)throw new Error(j.error||('HTTP '+r.status));return j}
async function load(){try{const [ifaces,current]=await Promise.all([api('/api/v1/deployment/interfaces'),api('/api/v1/deployment')]);q('iface').innerHTML='';for(const x of ifaces.interfaces){const o=document.createElement('option');o.value=x.name;o.textContent=x.name+(x.ipv4?' · '+x.ipv4:'')+(x.default?' · 默认路由':'');o.dataset.cidr=x.cidr||'';o.dataset.gateway=x.gateway||'';q('iface').appendChild(o)}if(current.configured){q('iface').value=current.spec.parent_interface;q('ip').value=current.spec.container_ip;q('subnet').value=current.spec.subnet;q('gateway').value=current.spec.gateway;q('data').value=current.spec.data_path;status.textContent=(current.running?'Gateway 正在运行\n':'Gateway 已配置但未运行\n')+(current.gateway_url||'')}else{const selected=q('iface').selectedOptions[0];if(selected){q('subnet').value=selected.dataset.cidr;q('gateway').value=selected.dataset.gateway}status.textContent='尚未创建 Gateway。'} }catch(e){status.textContent=e.message;status.classList.add('error')}}
q('iface').addEventListener('change',()=>{const x=q('iface').selectedOptions[0];if(x){if(!q('subnet').value)q('subnet').value=x.dataset.cidr;if(!q('gateway').value)q('gateway').value=x.dataset.gateway}});
q('apply').addEventListener('click',async()=>{q('apply').disabled=true;status.classList.remove('error');status.textContent='正在验证并创建 Gateway…';try{const result=await api('/api/v1/deployment',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({parent_interface:q('iface').value,container_ip:q('ip').value,subnet:q('subnet').value,gateway:q('gateway').value,data_path:q('data').value})});status.innerHTML='部署成功。Gateway：<a href="'+result.gateway_url+'">'+result.gateway_url+'</a>'}catch(e){status.textContent=e.message;status.classList.add('error')}finally{q('apply').disabled=false}});load();
</script></body></html>`
