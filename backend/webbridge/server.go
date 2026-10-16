package webbridge

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"go-stock/backend/uiruntime"
)

// Options 配置 Web 服务。
type Options struct {
	Dispatcher     *Dispatcher
	Assets         fs.FS    // 前端构建产物（frontend/dist）根目录
	Auth           *Auth    // 鉴权
	AllowedOrigins []string // 额外允许的 Origin（完整 scheme://host[:port]），默认仅同源
}

// Server 是 Web 服务的 HTTP 处理器。
type Server struct {
	opt     Options
	hub     *Hub
	allowed map[string]bool
	static  http.Handler
}

// NewServer 创建 Web 服务，并把 uiruntime 的事件出口接到 WebSocket 广播。
func NewServer(opt Options) *Server {
	s := &Server{opt: opt, allowed: map[string]bool{}}
	for _, o := range opt.AllowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			s.allowed[strings.ToLower(o)] = true
		}
	}
	s.hub = newHub(opt.Dispatcher, s.originOK)
	s.static = http.FileServer(http.FS(opt.Assets))
	uiruntime.SetEventSink(s.hub.Broadcast)
	return s
}

// Hub 返回事件总线（用于统计连接数）。
func (s *Server) Hub() *Hub { return s.hub }

// originOK 同源校验：浏览器发起的 WebSocket / POST 必须来自本站（防跨站 WebSocket 劫持 / CSRF）。
// 无 Origin 头（非浏览器客户端，如脚本）放行，但仍需通过鉴权。
func (s *Server) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if s.allowed[strings.ToLower(strings.TrimRight(origin, "/"))] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	if host == strings.ToLower(r.Host) {
		return true
	}
	if xf := r.Header.Get("X-Forwarded-Host"); xf != "" && host == strings.ToLower(strings.TrimSpace(strings.Split(xf, ",")[0])) {
		return true
	}
	return false
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Frame-Options", "SAMEORIGIN")

	p := r.URL.Path
	switch {
	case p == "/healthz":
		h.Set("Cache-Control", "no-store")
		_, _ = io.WriteString(w, "ok")
		return
	case p == "/login":
		s.opt.Auth.handleLogin(w, r)
		return
	case p == "/logout":
		s.opt.Auth.handleLogout(w, r)
		return
	}

	if !s.opt.Auth.Check(r) {
		s.unauthorized(w, r)
		return
	}

	switch {
	case p == "/ws":
		if !s.originOK(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		s.hub.serveWS(w, r)
	case p == "/api/call":
		s.handleCall(w, r)
	case p == "/api/methods":
		h.Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, s.opt.Dispatcher.Methods())
	case strings.HasPrefix(p, "/download/"):
		s.handleDownload(w, r)
	default:
		s.serveStatic(w, r)
	}
}

func (s *Server) unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p := r.URL.Path
	if strings.HasPrefix(p, "/api/") || p == "/ws" || strings.HasPrefix(p, "/download/") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if s.opt.Auth.PasswordEnabled() {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	http.Error(w, "未通过 Cloudflare Access 鉴权", http.StatusForbidden)
}

type callReq struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

// handleCall: POST /api/call {"method":"X","args":[...]} → {"result":...} | {"error":"..."}
// 必须带 X-Go-Stock-Web 头（触发 CORS 预检，阻止跨站表单/脚本直接调用）。
func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if r.Header.Get("X-Go-Stock-Web") == "" || !s.originOK(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	var req callReq
	if err := json.NewDecoder(io.LimitReader(r.Body, maxMessage)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := s.opt.Dispatcher.Call(req.Method, req.Args)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]json.RawMessage{"result": res})
}

// handleDownload: GET /download/<name> 下载 SaveFileDialog 导出的文件。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/download/")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	fp := filepath.Join(uiruntime.ExportsDir(), name)
	f, err := os.Open(fp)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	p := path.Clean("/" + r.URL.Path)
	if p != "/" {
		if _, err := fs.Stat(s.opt.Assets, strings.TrimPrefix(p, "/")); err != nil {
			// 前端使用 hash 路由；未知路径回退到 index.html
			p = "/"
		}
	}
	if p == "/" || p == "/index.html" {
		w.Header().Set("Cache-Control", "no-cache")
		b, err := fs.ReadFile(s.opt.Assets, "index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(b)))
		return
	}
	if strings.HasPrefix(p, "/assets/") {
		// Vite 产物文件名带内容哈希，可长期缓存
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = p
	s.static.ServeHTTP(w, r2)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
