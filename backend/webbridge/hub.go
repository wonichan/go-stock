package webbridge

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"go-stock/backend/logger"
	"go-stock/backend/uiruntime"

	"github.com/gorilla/websocket"
)

// 浏览器 ↔ 服务端 WebSocket 消息格式（JSON）：
//
//	→ {"t":"call","id":1,"m":"GetFollowList","a":[0]}   调用 App 方法
//	← {"t":"ret","id":1,"r":<result>} | {"t":"ret","id":1,"e":"错误信息"}
//	→ {"t":"emit","n":"updateSettings","d":[...]}      前端 EventsEmit 上行（仅通知 Go 监听器）
//	← {"t":"ev","n":"newChatStream","d":[...]}         后端 EventsEmit 广播
//
// 长耗时方法（AI 分析等）走 WebSocket 不受 Cloudflare 代理 HTTP 约 100 秒响应超时限制。
type wsIn struct {
	T  string            `json:"t"`
	ID int64             `json:"id,omitempty"`
	M  string            `json:"m,omitempty"`
	A  []json.RawMessage `json:"a,omitempty"`
	N  string            `json:"n,omitempty"`
	D  []any             `json:"d,omitempty"`
}

type wsRet struct {
	T  string          `json:"t"`
	ID int64           `json:"id"`
	R  json.RawMessage `json:"r,omitempty"`
	E  string          `json:"e,omitempty"`
}

type wsEvent struct {
	T string `json:"t"`
	N string `json:"n"`
	D []any  `json:"d"`
}

const (
	sendQueueSize = 4096
	pingInterval  = 30 * time.Second // 远小于 Cloudflare 空闲断开时间（约 100 秒）
	writeTimeout  = 15 * time.Second
	maxMessage    = 64 << 20
)

type client struct {
	conn *websocket.Conn
	send chan []byte
	once sync.Once
	done chan struct{}
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

// enqueue 非阻塞入队；队列满说明客户端过慢，断开让其重连（重连后前端会重新拉取数据）。
func (c *client) enqueue(b []byte) {
	select {
	case c.send <- b:
	case <-c.done:
	default:
		logger.SugaredLogger.Warnf("webbridge: client send queue full, disconnecting")
		c.close()
	}
}

// Hub 管理所有浏览器连接并广播后端事件。
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	disp    *Dispatcher
	up      websocket.Upgrader
}

func newHub(d *Dispatcher, checkOrigin func(r *http.Request) bool) *Hub {
	h := &Hub{clients: map[*client]struct{}{}, disp: d}
	h.up = websocket.Upgrader{
		ReadBufferSize:  32 << 10,
		WriteBufferSize: 32 << 10,
		CheckOrigin:     checkOrigin,
	}
	return h
}

// Broadcast 实现 uiruntime.EventSink。
func (h *Hub) Broadcast(name string, data []any) {
	if data == nil {
		data = []any{}
	}
	b, err := json.Marshal(wsEvent{T: "ev", N: name, D: data})
	if err != nil {
		logger.SugaredLogger.Warnf("webbridge: event %q marshal error: %v", name, err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.enqueue(b)
	}
}

// Clients 返回当前连接数。
func (h *Hub) Clients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade 已写入错误响应
	}
	c := &client{conn: conn, send: make(chan []byte, sendQueueSize), done: make(chan struct{})}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	logger.SugaredLogger.Infof("webbridge: ws connected from %s (clients=%d)", r.RemoteAddr, h.Clients())

	go h.writeLoop(c)
	h.readLoop(c)

	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
	logger.SugaredLogger.Infof("webbridge: ws disconnected %s (clients=%d)", r.RemoteAddr, h.Clients())
}

func (h *Hub) writeLoop(c *client) {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case b := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				c.close()
				return
			}
		case <-t.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		case <-c.done:
			return
		}
	}
}

func (h *Hub) readLoop(c *client) {
	c.conn.SetReadLimit(maxMessage)
	readWait := 3 * pingInterval
	_ = c.conn.SetReadDeadline(time.Now().Add(readWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(readWait))
	})
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(readWait))
		var msg wsIn
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.T {
		case "call":
			go func(m wsIn) {
				res, err := h.disp.Call(m.M, m.A)
				ret := wsRet{T: "ret", ID: m.ID, R: res}
				if err != nil {
					ret.R, ret.E = nil, err.Error()
				}
				b, _ := json.Marshal(ret)
				c.enqueue(b)
			}(msg)
		case "emit":
			uiruntime.DispatchFrontendEvent(msg.N, msg.D)
		}
	}
}
