package data

// 明文 http 数据源统一升级为 https。
//
// 背景：部分网络（如云主机/容器出口）屏蔽或劫持 80 端口，导致 http://qt.gtimg.cn 等行情源超时，
// 而同一主机的 https 均可用。为避免逐个修改散落在各文件中的 URL（上游几乎每天提交，改动越少越好），
// 这里在共享 resty 客户端上挂一个 OnBeforeRequest 钩子，按主机白名单把 http:// 改写为 https://。
//
// 开关（环境变量 GO_STOCK_HTTPS_UPGRADE）：
//   - on  ：对白名单主机升级（server 构建默认）
//   - off ：不改写（桌面构建默认，行为与上游一致）
//
// GO_STOCK_HTTPS_UPGRADE_HOSTS 可追加主机（逗号分隔）。白名单中的主机均已验证 https 返回 200。

import (
	"net/url"
	"os"
	"strings"
	"sync"

	"go-stock/backend/uiruntime"

	"github.com/go-resty/resty/v2"
)

var httpsUpgradeHosts = map[string]bool{
	"qt.gtimg.cn":                    true,
	"hq.sinajs.cn":                   true,
	"quotes.sina.cn":                 true,
	"vip.stock.finance.sina.com.cn":  true,
	"fund.eastmoney.com":             true,
	"api.fund.eastmoney.com":         true,
	"fundf10.eastmoney.com":          true,
	"emweb.securities.eastmoney.com": true,
	"d.10jqka.com.cn":                true,
	"api.tushare.pro":                true,
}

var (
	httpsUpgradeOnce    sync.Once
	httpsUpgradeEnabled bool
)

// HTTPSUpgradeEnabled 返回是否启用 http→https 升级（首次调用时读取环境变量）。
func HTTPSUpgradeEnabled() bool {
	httpsUpgradeOnce.Do(func() {
		switch strings.ToLower(strings.TrimSpace(os.Getenv("GO_STOCK_HTTPS_UPGRADE"))) {
		case "on", "1", "true", "yes":
			httpsUpgradeEnabled = true
		case "off", "0", "false", "no":
			httpsUpgradeEnabled = false
		default:
			httpsUpgradeEnabled = uiruntime.ServerMode
		}
		for _, h := range strings.Split(os.Getenv("GO_STOCK_HTTPS_UPGRADE_HOSTS"), ",") {
			if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
				httpsUpgradeHosts[h] = true
			}
		}
	})
	return httpsUpgradeEnabled
}

// UpgradeHTTPS 若启用且主机在白名单中，把 http:// URL 改写为 https://，否则原样返回。
// 也可供直接使用 net/http 的调用点复用。
func UpgradeHTTPS(rawURL string) string {
	if !strings.HasPrefix(rawURL, "http://") || !HTTPSUpgradeEnabled() {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Port() != "" || !httpsUpgradeHosts[strings.ToLower(u.Hostname())] {
		return rawURL
	}
	return "https://" + strings.TrimPrefix(rawURL, "http://")
}

// withHTTPSUpgrade 在 resty 客户端上注册 URL 改写钩子（在 resty 解析 URL 之前执行）。
func withHTTPSUpgrade(c *resty.Client) *resty.Client {
	return c.OnBeforeRequest(func(_ *resty.Client, r *resty.Request) error {
		r.URL = UpgradeHTTPS(r.URL)
		return nil
	})
}
