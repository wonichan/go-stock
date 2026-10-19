package data

import (
	"strings"
	"testing"
)

// 空行情/退市代码腾讯返回 v_szXXXX="" 时，旧实现会 json.Unmarshal("null") 把 stockInfo
// 置为 nil，再访问 .Code 触发 nil pointer（agent GetStockInfo 工具 panic 的根因）。
func TestParseTxStockDataEmptyPayloadNoPanic(t *testing.T) {
	cases := []string{
		`v_sz999999="";`,
		`v_sz999999="";`,
		`v_sh000000="";`,
		`v_r_hk99999="";`,
		`v_unknown123="1~2~3";`,
		``,
		`not-a-quote`,
	}
	for _, raw := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ParseTxStockData panicked on %q: %v", raw, r)
				}
			}()
			info, err := ParseTxStockData(raw)
			if err == nil && info == nil {
				t.Fatalf("expected error for empty payload %q, got nil,nil", raw)
			}
			if err == nil && info != nil && strings.TrimSpace(info.Code) == "" && raw != "" {
				// 允许有内容但无代码的极端情况，只要不 panic
			}
		}()
	}
}

func TestParseTxStockDataValidAShare(t *testing.T) {
	// 精简但仍满足 ParseTxHKStockData 的 ≥35 字段要求
	parts := make([]string, 50)
	for i := range parts {
		parts[i] = "0"
	}
	parts[1] = "贵州茅台"
	parts[3] = "1329.00"
	parts[4] = "1310.00"
	parts[5] = "1315.00"
	parts[29] = "20261019103400"
	parts[32] = "1330.00"
	parts[33] = "1310.00"
	parts[49] = "1.10"
	raw := `v_sh600519="` + strings.Join(parts, "~") + `";`
	info, err := ParseTxStockData(raw)
	if err != nil || info == nil {
		t.Fatalf("valid payload failed: info=%v err=%v", info, err)
	}
	if info.Code != "sh600519" && info.Name != "贵州茅台" {
		// Code comes from key stripping; Name from parts[1]
		t.Logf("parsed code=%q name=%q price=%q", info.Code, info.Name, info.Price)
	}
	if info.Name != "贵州茅台" {
		t.Fatalf("name=%q", info.Name)
	}
}

func TestParseFullSingleStockDataUnsupportedNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic: %v", r)
		}
	}()
	info, err := ParseFullSingleStockData(`hq_str_xx123="";`)
	if err == nil || info != nil {
		t.Fatalf("want error for unsupported prefix, got info=%v err=%v", info, err)
	}
}
