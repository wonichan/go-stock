//go:build server

package main

// Web 服务入口（go build -tags server）。
//
// 与桌面版共用 main() 中的全部初始化（数据目录、数据库、AutoMigrate 等），在创建 App 之后
// 由 runServer 接管：不创建 Wails 窗口，而是
//   - 以非 Wails 上下文调用 app.startup，并在进程启动时直接执行 app.domReady 中的后台任务
//     （行情刷新、快讯抓取、定时任务等，与是否有浏览器连接无关）；
//   - 通过 backend/webbridge 提供 前端静态资源 + /ws（WebSocket 调用与事件）+ /api/call（HTTP 回退）。
//
// 该构建不依赖 webkit/GTK，可用 CGO_ENABLED=0 静态编译。

import (
	"context"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/apppath"
	log "go-stock/backend/logger"
	"go-stock/backend/webbridge"
)

func init() {
	runServer = serveWeb
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// serverDeniedMethods Web 模式下屏蔽的桌面专属方法（可用 GO_STOCK_WEB_DENY_METHODS 追加，逗号分隔）。
func serverDeniedMethods() map[string]string {
	deny := map[string]string{
		"RestartAsAdmin": "桌面自更新/提权重启",
		"HideToTray":     "系统托盘",
		"ShowFromTray":   "系统托盘",
	}
	for _, m := range strings.Split(os.Getenv("GO_STOCK_WEB_DENY_METHODS"), ",") {
		if m = strings.TrimSpace(m); m != "" {
			deny[m] = "GO_STOCK_WEB_DENY_METHODS"
		}
	}
	return deny
}

func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func serveWeb(app *App) {
	listen := envOr("GO_STOCK_LISTEN", "127.0.0.1:8080")

	// DeepAgents 的 Shell 工具在服务器上等同远程命令执行：默认只读（上游已有开关），
	// 如确需写入可显式设置 GO_STOCK_SHELL_READONLY=0。
	if _, ok := os.LookupEnv("GO_STOCK_SHELL_READONLY"); !ok {
		_ = os.Setenv("GO_STOCK_SHELL_READONLY", "1")
	}

	sessionHours, _ := strconv.Atoi(envOr("GO_STOCK_WEB_SESSION_HOURS", "168"))
	auth := webbridge.NewAuth(
		os.Getenv("CF_ACCESS_TEAM_DOMAIN"),
		os.Getenv("CF_ACCESS_AUD"),
		os.Getenv("GO_STOCK_WEB_PASSWORD"),
		time.Duration(sessionHours)*time.Hour,
	)
	// 读取后从进程环境中移除密码，避免被子进程（Agent Shell、MCP stdio 等）读到
	_ = os.Unsetenv("GO_STOCK_WEB_PASSWORD")

	if !auth.Enabled() {
		if isLoopbackListen(listen) {
			log.SugaredLogger.Warnf("web: 未配置任何鉴权（GO_STOCK_WEB_PASSWORD / CF_ACCESS_*），仅因监听在回环地址 %s 而放行", listen)
		} else if os.Getenv("GO_STOCK_WEB_AUTH") == "none" {
			log.SugaredLogger.Warnf("web: GO_STOCK_WEB_AUTH=none，%s 上的服务没有任何鉴权！", listen)
		} else {
			log.SugaredLogger.Fatalf("web: 监听非回环地址 %s 时必须配置 GO_STOCK_WEB_PASSWORD 或 CF_ACCESS_TEAM_DOMAIN+CF_ACCESS_AUD（或显式 GO_STOCK_WEB_AUTH=none）", listen)
		}
	}
	log.SugaredLogger.Infof("web: auth cfAccess=%v password=%v", auth.CFAccessEnabled(), auth.PasswordEnabled())

	dist, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		log.SugaredLogger.Fatalf("web: frontend assets: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	disp := webbridge.NewDispatcher(ctx, app, serverDeniedMethods())
	var origins []string
	if v := os.Getenv("GO_STOCK_WEB_ALLOWED_ORIGINS"); v != "" {
		origins = strings.Split(v, ",")
	}
	srv := webbridge.NewServer(webbridge.Options{
		Dispatcher:     disp,
		Assets:         dist,
		Auth:           auth,
		AllowedOrigins: origins,
	})

	// 桌面版的 startup / domReady 生命周期：在进程启动时执行，后台任务不依赖浏览器连接
	app.startup(ctx)
	go app.domReady(ctx)

	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.SugaredLogger.Infof("web: go-stock server mode listening on http://%s (data dir: %s, methods: %d)",
		listen, apppath.BaseDir(), len(disp.Methods()))
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.SugaredLogger.Fatalf("web: listen %s: %v", listen, err)
	}
	app.shutdown(ctx)
}
