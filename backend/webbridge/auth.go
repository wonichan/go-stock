package webbridge

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-stock/backend/logger"
)

// Auth 负责请求鉴权，支持两种方式（可同时启用，任一通过即放行）：
//
//  1. Cloudflare Access：校验 Cf-Access-Jwt-Assertion 请求头（或 CF_Authorization Cookie）中的
//     RS256 JWT，公钥取自 https://<team>/cdn-cgi/access/certs，并校验 iss / aud / exp。
//  2. 密码：登录页提交 GO_STOCK_WEB_PASSWORD 后下发 HMAC 签名的会话 Cookie；
//     脚本可直接带 "Authorization: Bearer <密码>"。
type Auth struct {
	teamDomain string // 例如 myteam.cloudflareaccess.com
	aud        string
	password   string
	sessionKey []byte
	sessionTTL time.Duration

	keysMu    sync.RWMutex
	keys      map[string]*rsa.PublicKey
	keysFetch time.Time
	httpc     *http.Client

	loginMu sync.Mutex
}

const sessionCookie = "gostock_session"

// NewAuth 创建鉴权器。teamDomain 支持 "myteam"、"myteam.cloudflareaccess.com" 或带 https:// 前缀。
func NewAuth(teamDomain, aud, password string, sessionTTL time.Duration) *Auth {
	td := strings.TrimSpace(teamDomain)
	td = strings.TrimPrefix(strings.TrimPrefix(td, "https://"), "http://")
	td = strings.TrimSuffix(td, "/")
	if td != "" && !strings.Contains(td, ".") {
		td += ".cloudflareaccess.com"
	}
	if sessionTTL <= 0 {
		sessionTTL = 7 * 24 * time.Hour
	}
	a := &Auth{
		teamDomain: td,
		aud:        strings.TrimSpace(aud),
		password:   password,
		sessionTTL: sessionTTL,
		keys:       map[string]*rsa.PublicKey{},
		httpc:      &http.Client{Timeout: 10 * time.Second},
	}
	if password != "" {
		k := sha256.Sum256([]byte("go-stock-web-session:" + password))
		a.sessionKey = k[:]
	}
	return a
}

// CFAccessEnabled 是否启用 Cloudflare Access 校验。
func (a *Auth) CFAccessEnabled() bool { return a.teamDomain != "" && a.aud != "" }

// PasswordEnabled 是否启用密码登录。
func (a *Auth) PasswordEnabled() bool { return a.password != "" }

// Enabled 是否启用了任一鉴权方式。
func (a *Auth) Enabled() bool { return a.CFAccessEnabled() || a.PasswordEnabled() }

// Check 返回请求是否已通过鉴权。
func (a *Auth) Check(r *http.Request) bool {
	if !a.Enabled() {
		return true
	}
	if a.CFAccessEnabled() {
		tok := r.Header.Get("Cf-Access-Jwt-Assertion")
		if tok == "" {
			if c, err := r.Cookie("CF_Authorization"); err == nil {
				tok = c.Value
			}
		}
		if tok != "" {
			if err := a.verifyCFJWT(tok); err == nil {
				return true
			} else {
				logger.SugaredLogger.Warnf("webbridge: Cloudflare Access JWT 校验失败: %v", err)
			}
		}
	}
	if a.PasswordEnabled() {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(a.password)) == 1 {
				return true
			}
		}
		if c, err := r.Cookie(sessionCookie); err == nil && a.validSession(c.Value) {
			return true
		}
	}
	return false
}

func (a *Auth) sign(exp int64) string {
	m := hmac.New(sha256.New, a.sessionKey)
	m.Write([]byte("v1|" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

func (a *Auth) validSession(v string) bool {
	expStr, sig, ok := strings.Cut(v, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(exp)))
}

// handleLogin 处理 GET/POST /login。
func (a *Auth) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.PasswordEnabled() {
		http.Error(w, "密码登录未启用", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		pw := r.PostFormValue("password")
		a.loginMu.Lock() // 串行化 + 失败延迟，抑制暴力破解
		okPw := subtle.ConstantTimeCompare([]byte(pw), []byte(a.password)) == 1
		if !okPw {
			time.Sleep(time.Second)
		}
		a.loginMu.Unlock()
		if okPw {
			exp := time.Now().Add(a.sessionTTL).Unix()
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookie,
				Value:    strconv.FormatInt(exp, 10) + "." + a.sign(exp),
				Path:     "/",
				Expires:  time.Unix(exp, 0),
				HttpOnly: true,
				Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
				SameSite: http.SameSiteLaxMode,
			})
			http.Redirect(w, r, "./", http.StatusSeeOther)
			return
		}
		logger.SugaredLogger.Warnf("webbridge: 登录失败 from %s", r.RemoteAddr)
		w.WriteHeader(http.StatusUnauthorized)
		writeLoginPage(w, "密码错误")
		return
	}
	writeLoginPage(w, "")
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "login", http.StatusSeeOther)
}

func writeLoginPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	errHTML := ""
	if msg != "" {
		errHTML = `<p class="err">` + msg + `</p>`
	}
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>go-stock 登录</title>
<style>body{font-family:system-ui,sans-serif;background:#1b2636;color:#eee;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
form{background:#243247;padding:32px;border-radius:10px;min-width:280px}h1{font-size:20px;margin:0 0 16px}
input{width:100%%;box-sizing:border-box;padding:10px;margin:8px 0 16px;border-radius:6px;border:1px solid #456;background:#1b2636;color:#eee}
button{width:100%%;padding:10px;border:0;border-radius:6px;background:#3b82f6;color:#fff;font-size:15px;cursor:pointer}.err{color:#f87171}</style></head>
<body><form method="post" action="login"><h1>go-stock Web</h1>%s<label>访问密码<input type="password" name="password" autofocus autocomplete="current-password"></label><button type="submit">登录</button></form></body></html>`, errHTML)
}

// ---- Cloudflare Access JWT ----

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Aud any    `json:"aud"`
	Iss string `json:"iss"`
	Exp int64  `json:"exp"`
	Nbf int64  `json:"nbf"`
}

func b64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")) }

func (a *Auth) verifyCFJWT(tok string) error {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return errors.New("JWT 格式错误")
	}
	hb, err := b64(parts[0])
	if err != nil {
		return err
	}
	var hdr jwtHeader
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return err
	}
	if hdr.Alg != "RS256" {
		return fmt.Errorf("不支持的算法 %q", hdr.Alg)
	}
	key, err := a.publicKey(hdr.Kid)
	if err != nil {
		return err
	}
	sig, err := b64(parts[2])
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return errors.New("签名无效")
	}
	pb, err := b64(parts[1])
	if err != nil {
		return err
	}
	var c jwtClaims
	if err := json.Unmarshal(pb, &c); err != nil {
		return err
	}
	now := time.Now().Unix()
	const skew = 60
	if c.Exp == 0 || now > c.Exp+skew {
		return errors.New("JWT 已过期")
	}
	if c.Nbf != 0 && now+skew < c.Nbf {
		return errors.New("JWT 尚未生效")
	}
	if c.Iss != "https://"+a.teamDomain {
		return fmt.Errorf("iss 不匹配: %s", c.Iss)
	}
	audOK := false
	switch v := c.Aud.(type) {
	case string:
		audOK = v == a.aud
	case []any:
		for _, x := range v {
			if s, _ := x.(string); s == a.aud {
				audOK = true
			}
		}
	}
	if !audOK {
		return errors.New("aud 不匹配")
	}
	return nil
}

func (a *Auth) publicKey(kid string) (*rsa.PublicKey, error) {
	a.keysMu.RLock()
	k, ok := a.keys[kid]
	fresh := time.Since(a.keysFetch) < time.Hour
	a.keysMu.RUnlock()
	if ok && fresh {
		return k, nil
	}
	a.keysMu.Lock()
	defer a.keysMu.Unlock()
	// 未知 kid 时最多每 30 秒刷新一次，防止伪造 kid 打爆证书接口
	if k, ok := a.keys[kid]; ok && time.Since(a.keysFetch) < time.Hour {
		return k, nil
	}
	if time.Since(a.keysFetch) < 30*time.Second && len(a.keys) > 0 {
		if k, ok := a.keys[kid]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("未知 kid %q", kid)
	}
	keys, err := a.fetchKeys()
	if err != nil {
		if k, ok := a.keys[kid]; ok { // 拉取失败时沿用旧公钥
			return k, nil
		}
		return nil, err
	}
	a.keys, a.keysFetch = keys, time.Now()
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("未知 kid %q", kid)
}

func (a *Auth) fetchKeys() (map[string]*rsa.PublicKey, error) {
	resp, err := a.httpc.Get("https://" + a.teamDomain + "/cdn-cgi/access/certs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("获取 Access 公钥失败: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range body.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nb, err1 := b64(k.N)
		eb, err2 := b64(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		e := 0
		for _, b := range eb {
			e = e<<8 | int(b)
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}
	}
	if len(out) == 0 {
		return nil, errors.New("Access 证书接口未返回 RSA 公钥")
	}
	return out, nil
}
