package webbridge

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type demo struct{}

type pair struct {
	A int    `json:"a"`
	B string `json:"b"`
}

func (demo) Add(a, b int) int  { return a + b }
func (demo) Echo(p *pair) pair { return *p }
func (demo) Fail() error       { return errors.New("boom") }
func (demo) Maybe(ok bool) (string, error) {
	if ok {
		return "yes", nil
	}
	return "", errors.New("no")
}
func (demo) Ctx(ctx context.Context, s string) string {
	if ctx == nil {
		return "nil"
	}
	return "ctx:" + s
}
func (demo) Nothing() {}
func (demo) Panic()   { panic("bad") }

func raw(vs ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(vs))
	for i, v := range vs {
		out[i] = json.RawMessage(v)
	}
	return out
}

func TestDispatcher(t *testing.T) {
	d := NewDispatcher(context.Background(), demo{}, map[string]string{"Nothing": "test"})
	cases := []struct {
		m    string
		args []json.RawMessage
		want string
		err  string
	}{
		{"Add", raw("1", "2"), "3", ""},
		{"Echo", raw(`{"a":1,"b":"x"}`), `{"a":1,"b":"x"}`, ""},
		{"Fail", nil, "", "boom"},
		{"Maybe", raw("true"), `"yes"`, ""},
		{"Maybe", raw("false"), "", "no"},
		{"Ctx", raw(`"s"`), `"ctx:s"`, ""},
		{"Nothing", nil, "", "Web 模式下不可用"},
		{"Panic", nil, "", "执行异常"},
		{"Missing", nil, "", "未知方法"},
		{"Add", raw(`"x"`), "", "参数解析失败"},
	}
	for _, c := range cases {
		got, err := d.Call(c.m, c.args)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: want err %q, got %v / %s", c.m, c.err, err, got)
			}
			continue
		}
		if err != nil || string(got) != c.want {
			t.Errorf("%s: want %s, got %s / %v", c.m, c.want, got, err)
		}
	}
	for _, m := range d.Methods() {
		if m == "Nothing" {
			t.Error("denied method listed")
		}
	}
}

func TestPasswordSession(t *testing.T) {
	a := NewAuth("", "", "s3cret", time.Hour)
	r := httptest.NewRequest("GET", "/", nil)
	if a.Check(r) {
		t.Fatal("unauthenticated request accepted")
	}
	r.Header.Set("Authorization", "Bearer s3cret")
	if !a.Check(r) {
		t.Fatal("bearer rejected")
	}
	req := httptest.NewRequest("POST", "/login", strings.NewReader("password=s3cret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.handleLogin(w, req)
	cs := w.Result().Cookies()
	if len(cs) == 0 {
		t.Fatal("no session cookie")
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(cs[0])
	if !a.Check(r2) {
		t.Fatal("session cookie rejected")
	}
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.AddCookie(&http.Cookie{Name: sessionCookie, Value: cs[0].Value + "0"})
	if a.Check(r3) {
		t.Fatal("tampered cookie accepted")
	}
}

func TestCFAccessJWT(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "kty": "RSA",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	defer srv.Close()
	team := strings.TrimPrefix(srv.URL, "https://")
	a := NewAuth(team, "aud1", "", time.Hour)
	a.httpc = srv.Client()

	mk := func(claims map[string]any) string {
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1"})
		p, _ := json.Marshal(claims)
		s := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
		sum := sha256.Sum256([]byte(s))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		return s + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	now := time.Now().Unix()
	good := mk(map[string]any{"aud": []string{"aud1"}, "iss": "https://" + team, "exp": now + 600})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Cf-Access-Jwt-Assertion", good)
	if !a.Check(r) {
		t.Fatal("valid JWT rejected")
	}
	for name, tok := range map[string]string{
		"wrong aud": mk(map[string]any{"aud": "other", "iss": "https://" + team, "exp": now + 600}),
		"wrong iss": mk(map[string]any{"aud": "aud1", "iss": "https://evil", "exp": now + 600}),
		"expired":   mk(map[string]any{"aud": "aud1", "iss": "https://" + team, "exp": now - 3600}),
		"tampered":  good[:len(good)-4] + "AAAA",
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Cf-Access-Jwt-Assertion", tok)
		if a.Check(r) {
			t.Errorf("%s JWT accepted", name)
		}
	}
}

func TestOriginCheck(t *testing.T) {
	s := &Server{allowed: map[string]bool{"https://ok.example": true}}
	mk := func(host, origin string) *http.Request {
		r := httptest.NewRequest("GET", "http://"+host+"/ws", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if !s.originOK(mk("a.example", "https://a.example")) || !s.originOK(mk("a.example", "")) ||
		!s.originOK(mk("a.example", "https://ok.example")) {
		t.Fatal("same-origin / allowed origin rejected")
	}
	if s.originOK(mk("a.example", "https://evil.example")) {
		t.Fatal("cross-origin accepted")
	}
}
