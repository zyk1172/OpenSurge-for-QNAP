package webgateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookieName  = "opensurge_admin_session"
	sessionLifetime    = 12 * time.Hour
	loginWindow        = 15 * time.Minute
	maxLoginFailures   = 6
	bootstrapTokenFile = "bootstrap-token"
)

type Options struct {
	Addr                  string
	Upstream              string
	ControlToken          string
	AuthDir               string
	AllowedHosts          []string
	RequireBootstrapToken bool
	SecureCookies         bool
	QNAPOnly              bool
}

type session struct {
	Expires time.Time
}

type loginBucket struct {
	WindowStart time.Time
	Failures    int
}

type Server struct {
	addr                  string
	upstream              *url.URL
	controlToken          string
	authDir               string
	auth                  *AuthStore
	remoteTokens          *RemoteTokenStore
	proxy                 *httputil.ReverseProxy
	client                *http.Client
	allowedHosts          map[string]struct{}
	requireBootstrapToken bool
	secureCookies         bool
	qnapOnly              bool

	mu       sync.Mutex
	sessions map[string]session
	failures map[string]loginBucket
}

func New(options Options) (*Server, error) {
	if strings.TrimSpace(options.Addr) == "" {
		options.Addr = "0.0.0.0:8080"
	}
	if strings.TrimSpace(options.Upstream) == "" {
		options.Upstream = "http://127.0.0.1:61767"
	}
	if strings.TrimSpace(options.ControlToken) == "" {
		return nil, fmt.Errorf("control token is required")
	}
	if strings.TrimSpace(options.AuthDir) == "" {
		return nil, fmt.Errorf("auth directory is required")
	}
	upstream, err := url.Parse(options.Upstream)
	if err != nil || upstream.Scheme != "http" || upstream.Hostname() == "" {
		return nil, fmt.Errorf("invalid loopback control upstream %q", options.Upstream)
	}
	ip := net.ParseIP(upstream.Hostname())
	if upstream.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("control upstream must remain loopback-only")
	}

	allowed := map[string]struct{}{}
	for _, host := range options.AllowedHosts {
		host = normaliseHost(host)
		if host != "" {
			allowed[host] = struct{}{}
		}
	}

	s := &Server{
		addr:                  options.Addr,
		upstream:              upstream,
		controlToken:          options.ControlToken,
		authDir:               options.AuthDir,
		auth:                  NewAuthStore(options.AuthDir),
		remoteTokens:          NewRemoteTokenStore(options.AuthDir),
		client:                &http.Client{Timeout: 3 * time.Second},
		allowedHosts:          allowed,
		requireBootstrapToken: options.RequireBootstrapToken,
		secureCookies:         options.SecureCookies,
		qnapOnly:              options.QNAPOnly,
		sessions:              map[string]session{},
		failures:              map[string]loginBucket{},
	}
	if err := s.ensureBootstrapToken(); err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = upstream.Host
		req.Header.Set("Authorization", "Bearer "+s.controlToken)
		req.Header.Del("X-Forwarded-Host")
		req.Header.Del("X-Forwarded-Proto")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "control_unavailable", "message": err.Error()}})
	}
	s.proxy = proxy
	return s, nil
}

func (s *Server) ensureBootstrapToken() error {
	if !s.requireBootstrapToken {
		return nil
	}
	required, err := s.auth.SetupRequired()
	if err != nil {
		return err
	}
	path := filepath.Join(s.authDir, bootstrapTokenFile)
	if !required {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale bootstrap token: %w", err)
		}
		return nil
	}
	if data, err := os.ReadFile(path); err == nil {
		if strings.TrimSpace(string(data)) != "" {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read bootstrap token: %w", err)
	}
	token := randomToken(32)
	if err := writeDurableAtomic(path, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("create bootstrap token: %w", err)
	}
	fmt.Fprintf(os.Stderr, "OpenSurge first-run bootstrap token: %s\n", token)
	return nil
}

func (s *Server) verifyBootstrapToken(value string) bool {
	if !s.requireBootstrapToken {
		return true
	}
	data, err := os.ReadFile(filepath.Join(s.authDir, bootstrapTokenFile))
	if err != nil {
		return false
	}
	want := []byte(strings.TrimSpace(string(data)))
	got := []byte(strings.TrimSpace(value))
	return len(want) > 0 && subtle.ConstantTimeCompare(got, want) == 1
}

func (s *Server) clearBootstrapToken() {
	if !s.requireBootstrapToken {
		return
	}
	path := filepath.Join(s.authDir, bootstrapTokenFile)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "OpenSurge: remove consumed bootstrap token: %v\n", err)
	}
}

func (s *Server) Serve(ctx context.Context) error {
	server := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", s.handleLive)
	mux.HandleFunc("GET /health/ready", s.handleReady)
	mux.HandleFunc("GET /auth/", s.handleLoginPage)
	mux.HandleFunc("GET /api/auth/state", s.handleAuthState)
	mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/remote-token", s.handleRemoteTokenStatus)
	mux.HandleFunc("POST /api/auth/remote-token", s.handleRemoteTokenRotate)
	mux.HandleFunc("DELETE /api/auth/remote-token", s.handleRemoteTokenRevoke)
	mux.HandleFunc(remoteAPIPrefix, s.handleRemoteManagement)
	mux.HandleFunc(remoteAPIPrefix+"/", s.handleRemoteManagement)
	mux.HandleFunc("/", s.handleProxy)
	return s.securityHeaders(mux)
}

func (s *Server) handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "component": "web-gateway"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.upstream.String()+"/api/v1/config", nil)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready"})
		return
	}
	request.Host = s.upstream.Host
	request.Header.Set("Authorization", "Bearer "+s.controlToken)
	response, err := s.client.Do(request)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": "control_unavailable"})
		return
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "not_ready", "reason": "control_unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) handleAuthState(w http.ResponseWriter, _ *http.Request) {
	required, err := s.auth.SetupRequired()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auth_state_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_required":     required,
		"bootstrap_required": required && s.requireBootstrapToken,
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "origin_rejected"})
		return
	}
	var input struct {
		Username       string `json:"username"`
		Password       string `json:"password"`
		BootstrapToken string `json:"bootstrap_token,omitempty"`
	}
	if err := decodeJSON(r, &input, 8<<10); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if !s.verifyBootstrapToken(input.BootstrapToken) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid_bootstrap_token"})
		return
	}
	if err := s.auth.Setup(input.Username, input.Password); err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, ErrAlreadySetup) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	s.clearBootstrapToken()
	s.issueSession(w)
	writeJSON(w, http.StatusCreated, map[string]any{"authenticated": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "origin_rejected"})
		return
	}
	client := clientAddress(r)
	if retryAfter, blocked := s.loginBlocked(client); blocked {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(retryAfter.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too_many_attempts"})
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input, 8<<10); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_request"})
		return
	}
	if err := s.auth.Verify(input.Username, input.Password); err != nil {
		s.recordLoginFailure(client)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid_credentials"})
		return
	}
	s.clearLoginFailures(client)
	s.issueSession(w)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "origin_rejected"})
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.secureCookies})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.authenticated(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'")
	_, _ = w.Write([]byte(loginHTML))
}

func qnapPathBlocked(path string) bool {
	switch path {
	case "/api/v1/menubar", "/api/v1/sleep-prevention", "/api/v1/network/apply-static", "/api/v1/network/restore-dhcp":
		return true
	}
	return strings.HasPrefix(path, "/api/v1/sources/") && strings.HasSuffix(path, "/reveal")
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/bootstrap" || r.URL.Path == "/api/v1/session/bootstrap" {
		http.NotFound(w, r)
		return
	}
	if !s.authenticated(r) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "authentication_required", "message": "sign in to OpenSurge"}})
			return
		}
		http.Redirect(w, r, "/auth/", http.StatusFound)
		return
	}
	if s.qnapOnly && qnapPathBlocked(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "origin_rejected", "message": "mutation origin is not allowed"}})
		return
	}
	s.proxy.ServeHTTP(w, r)
}

func (s *Server) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.sessions[cookie.Value]
	if !ok || !now.Before(entry.Expires) {
		delete(s.sessions, cookie.Value)
		return false
	}
	entry.Expires = now.Add(sessionLifetime)
	s.sessions[cookie.Value] = entry
	return true
}

func (s *Server) issueSession(w http.ResponseWriter) {
	token := randomToken(32)
	s.mu.Lock()
	if len(s.sessions) >= 256 {
		now := time.Now()
		for key, entry := range s.sessions {
			if !now.Before(entry.Expires) {
				delete(s.sessions, key)
			}
		}
	sessions[token] = session{Expires: time.Now().Add(sessionLifetime)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionLifetime / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.secureCookies,
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "invalid_host"})
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if s.secureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostAllowed(value string) bool {
	host := normaliseHost(value)
	if host == "localhost" {
		return true
	}
	if _, ok := s.allowedHosts[host]; ok {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return false
}

func normaliseHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func sameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) loginBlocked(client string) (time.Duration, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.failures[client]
	if !ok || now.Sub(bucket.WindowStart) >= loginWindow {
		return 0, false
	}
	if bucket.Failures < maxLoginFailures {
		return 0, false
	}
	return loginWindow - now.Sub(bucket.WindowStart), true
}

func (s *Server) recordLoginFailure(client string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket := s.failures[client]
	if bucket.WindowStart.IsZero() || now.Sub(bucket.WindowStart) >= loginWindow {
		bucket = loginBucket{WindowStart: now}
	}
	bucket.Failures++
	s.failures[client] = bucket
}

func (s *Server) clearLoginFailures(client string) {
	s.mu.Lock()
	delete(s.failures, client)
	s.mu.Unlock()
}

func randomToken(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func decodeJSON(r *http.Request, target any, limit int64) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const loginHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>OpenSurge for QNAP</title><style>
:root{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color-scheme:light dark}body{margin:0;min-height:100vh;display:grid;place-items:center;background:#111827}main{width:min(420px,calc(100vw - 40px));background:#fff;color:#111827;padding:28px;border-radius:18px;box-shadow:0 24px 70px #0008}h1{font-size:24px;margin:0 0 8px}p{color:#6b7280;line-height:1.5}label{display:block;font-size:13px;font-weight:600;margin:16px 0 6px}input{box-sizing:border-box;width:100%;padding:12px;border:1px solid #d1d5db;border-radius:10px;font:inherit;background:#fff;color:#111827}button{width:100%;margin-top:20px;padding:12px;border:0;border-radius:10px;background:#111827;color:#fff;font-weight:700;cursor:pointer}.error{color:#b91c1c;min-height:1.5em;font-size:13px}@media(prefers-color-scheme:dark){main{background:#1f2937;color:#f9fafb}p{color:#9ca3af}input{background:#111827;color:#f9fafb;border-color:#4b5563}button{background:#f9fafb;color:#111827}}
</style></head><body><main><h1>OpenSurge for QNAP</h1><p id="hint">正在检查管理账户…</p><form id="form" hidden><label>用户名</label><input id="username" autocomplete="username" required minlength="3"><label>密码</label><input id="password" type="password" autocomplete="current-password" required minlength="12"><div id="bootstrap-wrap" hidden><label>首次启动令牌</label><input id="bootstrap" autocomplete="off"><p>令牌可在 OpenSurge 容器首次启动日志中查看。</p></div><button id="submit">登录</button><p class="error" id="error"></p></form></main><script>
(async()=>{const hint=document.getElementById('hint'),form=document.getElementById('form'),button=document.getElementById('submit'),error=document.getElementById('error'),bootstrapWrap=document.getElementById('bootstrap-wrap');try{const state=await fetch('/api/auth/state',{credentials:'same-origin'}).then(r=>r.json());const setup=!!state.setup_required,bootstrap=!!state.bootstrap_required;hint.textContent=setup?(bootstrap?'首次使用：输入容器日志中的启动令牌并创建管理员账户。':'首次使用：创建管理员账户。密码至少 12 个字符。'):'使用管理员账户登录。';button.textContent=setup?'创建管理员并进入':'登录';bootstrapWrap.hidden=!(setup&&bootstrap);form.hidden=false;form.addEventListener('submit',async e=>{e.preventDefault();error.textContent='';button.disabled=true;try{const payload={username:document.getElementById('username').value,password:document.getElementById('password').value};if(setup&&bootstrap)payload.bootstrap_token=document.getElementById('bootstrap').value;const response=await fetch(setup?'/api/auth/setup':'/api/auth/login',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});if(!response.ok){const body=await response.json().catch(()=>({}));throw new Error(body.error||'登录失败')}location.replace('/')}catch(err){error.textContent=err.message||'登录失败'}finally{button.disabled=false}})}catch(err){hint.textContent='认证服务不可用'}})();
</script></body></html>`
