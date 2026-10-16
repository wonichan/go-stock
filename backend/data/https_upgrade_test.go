package data

import "testing"

func TestUpgradeHTTPS(t *testing.T) {
	t.Setenv("GO_STOCK_HTTPS_UPGRADE", "on")
	httpsUpgradeEnabled = true
	cases := map[string]string{
		"http://qt.gtimg.cn/?_=1&q=sh600519": "https://qt.gtimg.cn/?_=1&q=sh600519",
		"http://hq.sinajs.cn/list=sh600519":  "https://hq.sinajs.cn/list=sh600519",
		"http://example.com/x":               "http://example.com/x",
		"http://qt.gtimg.cn:8080/x":          "http://qt.gtimg.cn:8080/x",
		"https://qt.gtimg.cn/x":              "https://qt.gtimg.cn/x",
	}
	httpsUpgradeOnce.Do(func() {}) // 跳过环境变量读取，直接使用上面设定的开关
	for in, want := range cases {
		if got := UpgradeHTTPS(in); got != want {
			t.Errorf("UpgradeHTTPS(%q) = %q, want %q", in, got, want)
		}
	}
	httpsUpgradeEnabled = false
	if got := UpgradeHTTPS("http://qt.gtimg.cn/x"); got != "http://qt.gtimg.cn/x" {
		t.Errorf("disabled upgrade rewrote URL: %s", got)
	}
}
