//go:build server

package uiruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go-stock/backend/logger"
)

// ServerMode 为 true：Web 服务构建，没有 Wails 窗口上下文。
const ServerMode = true

// ErrNotSupported 表示该桌面能力在 Web 模式下尚未实现（如原生打开文件对话框）。
var ErrNotSupported = errors.New("Web 模式暂不支持该桌面功能（原生文件对话框/窗口操作）")

type listener struct {
	id uint64
	cb func(optionalData ...interface{})
}

var (
	mu        sync.RWMutex
	sink      EventSink
	listeners = map[string][]listener{}
	nextID    atomic.Uint64
)

// SetEventSink 设置后端→前端事件出口（webbridge 的 WebSocket 广播）。
func SetEventSink(s EventSink) {
	mu.Lock()
	sink = s
	mu.Unlock()
}

// DispatchFrontendEvent 处理前端 EventsEmit 上行的事件：只通知 Go 侧监听器，
// 与 Wails 语义一致（前端本地监听器已由 JS shim 在浏览器内直接触发）。
func DispatchFrontendEvent(eventName string, data []interface{}) {
	notifyGo(eventName, data)
}

func notifyGo(eventName string, data []interface{}) {
	mu.RLock()
	ls := append([]listener(nil), listeners[eventName]...)
	mu.RUnlock()
	for _, l := range ls {
		go func(cb func(...interface{})) {
			defer func() {
				if r := recover(); r != nil {
					logger.SugaredLogger.Errorf("uiruntime: event %q listener panic: %v", eventName, r)
				}
			}()
			cb(data...)
		}(l.cb)
	}
}

// EventsEmit 与 Wails 一致：同时通知 Go 侧监听器与所有前端（浏览器）连接。
func EventsEmit(_ context.Context, eventName string, optionalData ...interface{}) {
	notifyGo(eventName, optionalData)
	mu.RLock()
	s := sink
	mu.RUnlock()
	if s != nil {
		s(eventName, optionalData)
	}
}

func EventsOn(_ context.Context, eventName string, callback func(optionalData ...interface{})) func() {
	id := nextID.Add(1)
	mu.Lock()
	listeners[eventName] = append(listeners[eventName], listener{id: id, cb: callback})
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		ls := listeners[eventName]
		for i, l := range ls {
			if l.id == id {
				listeners[eventName] = append(ls[:i:i], ls[i+1:]...)
				return
			}
		}
	}
}

func EventsOff(_ context.Context, eventName string, additionalEventNames ...string) {
	mu.Lock()
	defer mu.Unlock()
	delete(listeners, eventName)
	for _, n := range additionalEventNames {
		delete(listeners, n)
	}
}

// ---- 对话框 ----

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N}._\-()\[\] ]+`)

// SaveFileDialog 在 Web 模式下不弹窗：返回 <数据根>/exports 下的唯一路径，
// 业务代码照常写文件；写入完成（大小稳定）后向浏览器推送 "web:download" 事件触发下载。
func SaveFileDialog(ctx context.Context, opts SaveDialogOptions) (string, error) {
	dir := ExportsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := filepath.Base(strings.TrimSpace(opts.DefaultFilename))
	name = strings.TrimSpace(unsafeName.ReplaceAllString(name, "_"))
	if name == "" || name == "." || name == ".." {
		name = "export"
	}
	name = fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), name)
	path := filepath.Join(dir, name)
	go watchExport(ctx, path, name)
	return path, nil
}

// watchExport 轮询导出文件，大小连续稳定后通知前端下载（最长等待 10 分钟）。
func watchExport(ctx context.Context, path, name string) {
	var last int64 = -1
	stable := 0
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		if fi.Size() > 0 && fi.Size() == last {
			stable++
			if stable >= 2 {
				EventsEmit(ctx, "web:download", map[string]interface{}{
					"name": name,
					"url":  "download/" + name,
					"size": fi.Size(),
				})
				return
			}
		} else {
			stable = 0
		}
		last = fi.Size()
	}
}

func OpenFileDialog(context.Context, OpenDialogOptions) (string, error) {
	return "", ErrNotSupported
}

func OpenMultipleFilesDialog(context.Context, OpenDialogOptions) ([]string, error) {
	return nil, ErrNotSupported
}

func MessageDialog(context.Context, MessageDialogOptions) (string, error) {
	return "", ErrNotSupported
}

// BrowserOpenURL 交给浏览器端 shim 用 window.open 打开。
func BrowserOpenURL(ctx context.Context, url string) {
	EventsEmit(ctx, "web:openURL", url)
}

// Quit 在 Web 模式下不退出服务进程（浏览器端「退出」不应结束服务器）。
func Quit(context.Context) {
	logger.SugaredLogger.Infof("uiruntime: Quit ignored in server mode")
}

func ScreenGetAll(context.Context) ([]Screen, error) { return nil, nil }

// ---- 窗口操作：Web 模式下无原生窗口 ----

func WindowSetBackgroundColour(_ context.Context, _, _, _, _ uint8) {}
func WindowSetDarkTheme(context.Context)                            {}
func WindowSetLightTheme(context.Context)                           {}

// WindowReloadApp 通知浏览器刷新页面（如设置更新后切换主题）。
func WindowReloadApp(ctx context.Context)      { EventsEmit(ctx, "web:reload") }
func WindowShow(context.Context)               {}
func WindowHide(context.Context)               {}
func WindowUnminimise(context.Context)         {}
func WindowFullscreen(context.Context)         {}
func WindowUnfullscreen(context.Context)       {}
func WindowCenter(context.Context)             {}
func WindowSetSize(context.Context, int, int)  {}
func WindowGetSize(context.Context) (int, int) { return 0, 0 }
