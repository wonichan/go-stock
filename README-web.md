# go-stock Web 版（自托管）

在服务器/NAS 上以 **Web 服务** 方式运行 go-stock，通过浏览器访问；推荐用 **Cloudflare Tunnel + Cloudflare Access** 暴露到公网，宿主机不开放任何端口。

> 本模式基于同一份代码，用 `-tags server` 构建：不依赖 webkit/GTK，可 `CGO_ENABLED=0` 静态编译。
> 桌面版（`wails build`）不受影响。

## 工作原理

| 组件 | 说明 |
|---|---|
| `main_server.go`（`-tags server`） | 复用桌面版 `main()` 的全部初始化，创建 `App` 后不启动 Wails 窗口，改为启动 HTTP 服务；`startup` / `domReady` 中的后台任务（行情刷新、快讯抓取、定时任务、信号监控）在进程启动时即运行，与是否有浏览器连接无关 |
| `backend/webbridge` | 反射调用绑定给 Wails 的 `*App` 全部导出方法（参数/返回值语义与 Wails 一致）；`/ws` WebSocket 承载调用与事件推送（首选），`POST /api/call` 为 HTTP 回退 |
| `backend/uiruntime` | Wails runtime 的薄封装：桌面构建原样转发；server 构建把 `EventsEmit` 广播到浏览器，窗口/托盘/对话框等桌面能力降级为 no-op |
| `frontend/src/web-shim.js` | 浏览器中（无 Wails 注入时）实现 `window.go.main.App.*` 与 `window.runtime.*`，Vue 代码无需改动；桌面版中不生效 |

## 构建

### 方式一：Docker（推荐）

```bash
docker build -t go-stock-web .
# 可选：注入版本号
docker build --build-arg VERSION=$(git describe --tags --always) --build-arg VERSION_COMMIT=$(git rev-parse --short HEAD) -t go-stock-web .
```

镜像为多阶段构建：`node` 构建前端 → `golang` 以 `-tags server` 编译 → `debian:trixie-slim` + `chromium` + `fonts-noto-cjk`（用于网页抓取与 Markdown 转图片的中文字体）。

### 方式二：直接编译二进制

```bash
cd frontend && npm ci && npm run build && cd ..
CGO_ENABLED=0 go build -tags server -trimpath -ldflags "-s -w" -o go-stock-server .
```

运行机需要安装 Chrome/Chromium（部分数据抓取依赖），否则相关功能不可用。

## 运行

### Docker Compose + Cloudflare Tunnel

```bash
cp .env.web.example .env      # 填写 TUNNEL_TOKEN、CF_ACCESS_TEAM_DOMAIN、CF_ACCESS_AUD
docker compose up -d --build
docker compose logs -f go-stock
```

- `docker-compose.yml` 包含 `go-stock` 与 `cloudflared` 两个服务，**默认不发布任何端口**；cloudflared 主动外连 Cloudflare。
- 数据保存在命名卷 `go-stock-data`（容器内 `/data`，即 `GO_STOCK_ROOT_DIR`），包括 SQLite 数据库、日志、技能、知识库、导出文件。**其中含有 AI API Key 等敏感配置，请妥善备份与保护。**
- 若改用宿主机目录挂载（`./data:/data`），需先 `sudo chown -R 10001:10001 ./data`（容器以 uid 10001 运行）。
- 如需局域网直连，取消 compose 中 `ports` 的注释（建议仅绑定 `127.0.0.1` 或内网地址，并配置密码）。

### 直接运行二进制

```bash
GO_STOCK_ROOT_DIR=/srv/go-stock \
GO_STOCK_LISTEN=127.0.0.1:8080 \
GO_STOCK_WEB_PASSWORD='一个足够长的随机密码' \
./go-stock-server
```

浏览器打开 `http://127.0.0.1:8080`，输入密码登录。

### 迁移桌面版数据

把桌面版数据目录中的 `data/stock.db` 复制到 `/data/data/stock.db`（容器）或 `$GO_STOCK_ROOT_DIR/data/stock.db`。**复制前先退出桌面版**（或使用 `sqlite3 stock.db ".backup new.db"`），避免 WAL 未合并。迁移后在「设置」中把浏览器路径改为服务器上的路径（镜像内为 `/usr/bin/chromium`）。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `GO_STOCK_LISTEN` | `127.0.0.1:8080`（镜像内 `0.0.0.0:8080`） | 监听地址 |
| `GO_STOCK_ROOT_DIR` | 可执行文件目录/用户配置目录（镜像内 `/data`） | 数据根目录（`data/`、`logs/`、`exports/` 等） |
| `CF_ACCESS_TEAM_DOMAIN` | – | Cloudflare Access 团队域名，如 `myteam.cloudflareaccess.com`（也可只写 `myteam`） |
| `CF_ACCESS_AUD` | – | Access 应用的 Application Audience (AUD) Tag |
| `GO_STOCK_WEB_PASSWORD` | – | 后备密码登录；也可用 `Authorization: Bearer <密码>` 供脚本调用。读取后会从进程环境中移除 |
| `GO_STOCK_WEB_SESSION_HOURS` | `168` | 密码登录会话有效期（小时） |
| `GO_STOCK_WEB_AUTH` | – | 设为 `none` 才允许在非回环地址上**无鉴权**运行（不推荐） |
| `GO_STOCK_WEB_ALLOWED_ORIGINS` | – | 额外允许的 Origin（逗号分隔，完整 `https://host`）；默认只允许同源 |
| `GO_STOCK_WEB_DENY_METHODS` | – | 额外屏蔽的后端方法名（逗号分隔） |
| `GO_STOCK_SHELL_READONLY` | server 构建默认 `1` | DeepAgents Shell 工具只读模式（上游已有开关）；设为 `0` 恢复可写 |
| `GO_STOCK_HTTPS_UPGRADE` | server 构建默认 `on`，桌面默认 `off` | 把白名单内行情源的 `http://` 请求自动升级为 `https://` |
| `GO_STOCK_HTTPS_UPGRADE_HOSTS` | – | 追加需要升级为 https 的主机（逗号分隔） |
| `TZ` | `Asia/Shanghai` | 时区（交易时间判断依赖本地时间） |

鉴权规则：配置了 Access 时校验 `Cf-Access-Jwt-Assertion` 请求头（或 `CF_Authorization` Cookie）中的 JWT（RS256，公钥取自 `https://<团队域名>/cdn-cgi/access/certs`，校验 `iss`/`aud`/`exp`）；配置了密码时也接受密码会话。两者任一通过即可。**两者都未配置时，只允许监听回环地址。** `/healthz` 不需要鉴权。

## Cloudflare Tunnel + Access 配置

1. **创建 Tunnel**：Cloudflare 控制台 → Zero Trust → Networks → Tunnels → Create a tunnel → 选择 Cloudflared → 复制 token 填入 `.env` 的 `TUNNEL_TOKEN`。
2. **添加 Public Hostname**：在该 Tunnel 下添加，例如 `stock.example.com`，Service 选 `HTTP`，URL 填 `go-stock:8080`（compose 内的服务名）。WebSocket 默认支持，无需额外设置。
3. **创建 Access 应用**：Zero Trust → Access → Applications → Add an application → Self-hosted，域名填 `stock.example.com`；策略（Policy）设为 Allow，规则选 Emails = 你的邮箱（或你的 IdP 组）。会话时长按需设置（如 24h）。
4. **记录 AUD 与团队域名**：应用详情 → Overview 中复制 **Application Audience (AUD) Tag** 填入 `CF_ACCESS_AUD`；团队域名（Zero Trust → Settings → Custom Pages 中的 `<team>.cloudflareaccess.com`）填入 `CF_ACCESS_TEAM_DOMAIN`。这样即使有人绕过 Cloudflare 直连源站，也会因没有合法 JWT 被拒绝（纵深防御）。
5. **不要缓存 `/api` 与 `/ws`**：服务端已对 `/api/*`、`/ws`、`/download/*`、`index.html` 返回 `Cache-Control: no-store/no-cache`，Cloudflare 默认也不缓存这些路径。如果你配置了 “Cache Everything” 之类的 Cache Rule，务必为 `/api/*`、`/ws`、`/download/*`、`/login` 添加 **Bypass cache** 规则。`/assets/*` 文件名带内容哈希，可放心长期缓存。
6. **超时说明**：Cloudflare 对代理的 HTTP 响应约 100 秒超时（524）。AI 分析等长耗时调用默认走 WebSocket，不受此限制；服务端每 30 秒发送 ping 保持连接。

## 功能状态

可用（已验证）：首页/市场总览、自选股（增删、实时价格推送）、行情、快讯/新闻、设置（含 AI 模型配置）、AI 流式分析（自备 OpenAI 兼容模型）。
其余页面多数可直接使用（同一套后端方法），但以下能力在 Web 版中尚未实现或被禁用：

- 原生“打开文件”对话框（导入技能包、导入交易记录 Excel、知识库选择文件）：返回“Web 模式暂不支持”。
- 原生“保存文件”对话框：改为保存到 `/data/exports/`，写完后浏览器自动下载（`/download/<文件名>`）。
- 系统托盘、桌面通知、单实例锁、自动更新、关闭确认、窗口尺寸记忆：不适用，已禁用或为 no-op。需要通知请使用钉钉/飞书推送。
- 自动更新：Web 构建与「定制版」一样不检查/下载新版本（升级请重新构建镜像），赞助码/VIP 判定逻辑不变。
- 内置的独立 `ai-assistant-web`（:18888）在 server 模式下不启动，只暴露一个端口。

## 安全须知

- 应用为**单用户**设计：一个数据库、一份设置，所有登录的浏览器共享同一状态。请只给自己授权。
- 后端暴露约 400 个方法（包括修改设置、MCP 服务器、Agent 工具等），**切勿无鉴权暴露到公网**。
- DeepAgents 的 Shell 工具在 server 模式下默认只读；MCP（stdio）服务器会在容器内启动你配置的命令，请谨慎配置。
- 容器以非 root（uid 10001）运行；Chromium 以 `--no-sandbox` 运行（容器内通常无可用沙箱），只用于抓取行情/资讯页面。

## 赞助/VIP

Web 构建对赞助码与 VIP 功能的判定**与上游完全一致**，没有任何绕过。自行构建的二进制/镜像没有官方注入的 `BuildKey`，因此赞助码无法通过校验，VIP 功能保持锁定（与任何自行编译的桌面版相同）。

## 许可证

GPL-3.0。自用无额外义务；若分发镜像或二进制，需按 GPL-3.0 提供对应源代码（含本修改）。
