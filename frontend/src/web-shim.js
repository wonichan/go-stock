// go-stock Web 模式运行时垫片（server 构建）。
//
// Wails 生成的 wailsjs/go/main/App.js 只是 window.go.main.App.X(...)，
// wailsjs/runtime/runtime.js 只是 window.runtime.X(...)。在浏览器中（没有 Wails 注入的
// window.runtime 时）这里用 WebSocket/HTTP 实现这两个对象，因此 Vue 业务代码无需改动。
// 桌面版（Wails 已注入 window.runtime）中本文件不做任何事。

const NOOP = () => {}
const resolved = (v) => Promise.resolve(v)

function install() {
    const base = new URL('.', window.location.href)
    const wsURL = new URL('ws', base)
    wsURL.protocol = wsURL.protocol === 'https:' ? 'wss:' : 'ws:'
    const callURL = new URL('api/call', base).toString()

    // ---------------- 事件（与 Wails runtime 语义一致） ----------------
    const listeners = new Map() // name -> [{cb, max}]

    function notifyLocal(name, data) {
        const ls = listeners.get(name)
        if (!ls) return
        for (const l of ls.slice()) {
            try {
                l.cb.apply(null, data || [])
            } catch (e) {
                console.error('[web-shim] listener error', name, e)
            }
            if (l.max > 0 && --l.max === 0) {
                const cur = listeners.get(name) || []
                const i = cur.indexOf(l)
                if (i >= 0) cur.splice(i, 1)
                if (cur.length === 0) listeners.delete(name)
            }
        }
    }

    function EventsOnMultiple(name, cb, max) {
        const l = {cb, max: max === undefined ? -1 : max}
        if (!listeners.has(name)) listeners.set(name, [])
        listeners.get(name).push(l)
        return () => {
            const cur = listeners.get(name) || []
            const i = cur.indexOf(l)
            if (i >= 0) cur.splice(i, 1)
            if (cur.length === 0) listeners.delete(name)
        }
    }

    // ---------------- 连接与调用 ----------------
    let ws = null
    let wsOpen = false
    let nextId = 1
    let backoff = 1000
    const pending = new Map() // id -> {resolve, reject}
    let openWaiters = []

    function connect() {
        ws = new WebSocket(wsURL.toString())
        ws.onopen = () => {
            const reconnect = backoff !== 1000 || window.__goStockWebConnectedOnce
            wsOpen = true
            backoff = 1000
            window.__goStockWebConnectedOnce = true
            openWaiters.forEach((w) => w(true))
            openWaiters = []
            if (reconnect) notifyLocal('web:reconnected', [])
        }
        ws.onmessage = (e) => {
            let msg
            try {
                msg = JSON.parse(e.data)
            } catch (_) {
                return
            }
            if (msg.t === 'ret') {
                const p = pending.get(msg.id)
                if (!p) return
                pending.delete(msg.id)
                if (msg.e) p.reject(msg.e)
                else p.resolve(msg.r === undefined ? null : msg.r)
            } else if (msg.t === 'ev') {
                handleServerEvent(msg.n, msg.d || [])
            }
        }
        ws.onclose = () => {
            const wasOpen = wsOpen
            wsOpen = false
            for (const [id, p] of pending) {
                p.reject('与服务器的连接已断开')
                pending.delete(id)
            }
            openWaiters.forEach((w) => w(false))
            openWaiters = []
            if (!wasOpen) checkAuth()
            setTimeout(connect, backoff)
            backoff = Math.min(backoff * 2, 15000)
        }
    }

    // WebSocket 握手失败时无法读取 HTTP 状态码，用一次普通请求判断是否需要登录
    function checkAuth() {
        fetch(new URL('api/methods', base), {credentials: 'same-origin'}).then((r) => {
            if (r.status === 401) window.location.href = new URL('login', base).toString()
        }).catch(NOOP)
    }

    function waitOpen(ms) {
        if (wsOpen) return resolved(true)
        return new Promise((resolve) => {
            const t = setTimeout(() => resolve(false), ms)
            openWaiters.push((ok) => {
                clearTimeout(t)
                resolve(ok)
            })
        })
    }

    async function httpCall(method, args) {
        const r = await fetch(callURL, {
            method: 'POST',
            credentials: 'same-origin',
            headers: {'Content-Type': 'application/json', 'X-Go-Stock-Web': '1'},
            body: JSON.stringify({method, args}),
        })
        if (r.status === 401) {
            window.location.href = new URL('login', base).toString()
            throw 'unauthorized'
        }
        const body = await r.json()
        if (body.error) throw body.error
        return body.result === undefined ? null : body.result
    }

    // 优先 WebSocket（不受 Cloudflare 代理 HTTP 约 100s 超时限制），不可用时回退 HTTP POST
    async function call(method, args) {
        if (await waitOpen(3000)) {
            return new Promise((resolve, reject) => {
                const id = nextId++
                pending.set(id, {resolve, reject})
                try {
                    ws.send(JSON.stringify({t: 'call', id, m: method, a: args}))
                } catch (e) {
                    pending.delete(id)
                    httpCall(method, args).then(resolve, reject)
                }
            })
        }
        return httpCall(method, args)
    }

    function sendEmit(name, data) {
        if (wsOpen) {
            try {
                ws.send(JSON.stringify({t: 'emit', n: name, d: data}))
            } catch (_) { /* ignore */ }
        }
    }

    function handleServerEvent(name, data) {
        switch (name) {
            case 'web:openURL':
                if (data[0]) window.open(data[0], '_blank', 'noopener')
                return
            case 'web:reload':
                window.location.reload()
                return
            case 'web:download': {
                const info = data[0] || {}
                if (!info.url) return
                const a = document.createElement('a')
                a.href = new URL(info.url, base).toString()
                a.download = info.name || ''
                document.body.appendChild(a)
                a.click()
                a.remove()
                return
            }
        }
        notifyLocal(name, data)
    }

    // ---------------- window.go ----------------
    // 少数方法在浏览器端直接实现，避免服务端广播到所有标签页
    const localOverrides = {
        OpenURL: (url) => {
            window.open(url, '_blank', 'noopener')
            return resolved(null)
        },
        QuitApp: () => resolved(null),
    }
    const App = new Proxy({}, {
        get(_, name) {
            if (typeof name !== 'string') return undefined
            if (localOverrides[name]) return localOverrides[name]
            return (...args) => call(name, args)
        },
    })
    window.go = {main: {App}}

    // ---------------- window.runtime ----------------
    const log = (fn) => (m) => fn('[go-stock]', m)
    let fullscreen = false
    window.runtime = {
        LogPrint: log(console.log),
        LogTrace: log(console.debug),
        LogDebug: log(console.debug),
        LogInfo: log(console.info),
        LogWarning: log(console.warn),
        LogError: log(console.error),
        LogFatal: log(console.error),

        EventsOnMultiple,
        EventsOn: (name, cb) => EventsOnMultiple(name, cb, -1),
        EventsOnce: (name, cb) => EventsOnMultiple(name, cb, 1),
        EventsOff: (name, ...more) => {
            listeners.delete(name)
            more.forEach((n) => listeners.delete(n))
        },
        EventsOffAll: () => listeners.clear(),
        // Wails 语义：前端 Emit 同时通知前端本地监听器与 Go 监听器
        EventsEmit: (name, ...data) => {
            notifyLocal(name, data)
            sendEmit(name, data)
        },

        Environment: () => resolved({buildType: 'production', platform: 'web', arch: 'web'}),
        BrowserOpenURL: (url) => {
            window.open(url, '_blank', 'noopener')
        },
        Quit: NOOP,
        Hide: NOOP,
        Show: NOOP,

        WindowReload: () => window.location.reload(),
        WindowReloadApp: () => window.location.reload(),
        WindowSetTitle: (t) => {
            document.title = t
        },
        WindowFullscreen: () => {
            fullscreen = true
            document.documentElement.requestFullscreen?.().catch(NOOP)
        },
        WindowUnfullscreen: () => {
            fullscreen = false
            if (document.fullscreenElement) document.exitFullscreen?.().catch(NOOP)
        },
        WindowIsFullscreen: () => resolved(!!document.fullscreenElement || fullscreen),
        WindowGetSize: () => resolved({w: window.innerWidth, h: window.innerHeight}),
        WindowGetPosition: () => resolved({x: 0, y: 0}),
        WindowIsMaximised: () => resolved(false),
        WindowIsMinimised: () => resolved(false),
        WindowIsNormal: () => resolved(true),
        ScreenGetAll: () => resolved([{isCurrent: true, isPrimary: true, width: screen.width, height: screen.height}]),
        WindowCenter: NOOP, WindowSetAlwaysOnTop: NOOP, WindowSetSystemDefaultTheme: NOOP,
        WindowSetLightTheme: NOOP, WindowSetDarkTheme: NOOP, WindowSetSize: NOOP, WindowSetMaxSize: NOOP,
        WindowSetMinSize: NOOP, WindowSetPosition: NOOP, WindowHide: NOOP, WindowShow: NOOP,
        WindowMaximise: NOOP, WindowToggleMaximise: NOOP, WindowUnmaximise: NOOP, WindowMinimise: NOOP,
        WindowUnminimise: NOOP, WindowSetBackgroundColour: NOOP,

        ClipboardGetText: () => navigator.clipboard?.readText?.() ?? resolved(''),
        ClipboardSetText: (t) => (navigator.clipboard?.writeText?.(t) ?? resolved()).then(() => true, () => false),
        OnFileDrop: NOOP, OnFileDropOff: NOOP,
        CanResolveFilePaths: () => false,
        ResolveFilePaths: NOOP,

        InitializeNotifications: () => resolved(), CleanupNotifications: NOOP,
        IsNotificationAvailable: () => resolved(false),
        RequestNotificationAuthorization: () => resolved(false),
        CheckNotificationAuthorization: () => resolved(false),
        SendNotification: () => resolved(), SendNotificationWithActions: () => resolved(),
        RegisterNotificationCategory: () => resolved(), RemoveNotificationCategory: () => resolved(),
        RemoveAllPendingNotifications: () => resolved(), RemovePendingNotification: () => resolved(),
        RemoveAllDeliveredNotifications: () => resolved(), RemoveDeliveredNotification: () => resolved(),
        RemoveNotification: () => resolved(),
    }
    window.__GO_STOCK_WEB__ = true
    connect()
}

if (typeof window !== 'undefined' && !window.runtime) {
    install()
}
