package provider

import "testing"

func TestLookupCaseInsensitive(t *testing.T) {
	m := map[string]map[string]any{"0xAbC": {"a": 1}}
	if v, ok := lookupCaseInsensitive(m, "0xAbC"); !ok || v["a"] != 1 {
		t.Fatal("精确命中失败")
	}
	if v, ok := lookupCaseInsensitive(m, "0xabc"); !ok || v["a"] != 1 {
		t.Fatal("大小写不敏感命中失败")
	}
	// 注意：单键表会在下方"兜底"分支命中，因此用多键表验证未命中语义
	miss := map[string]map[string]any{"0xAbC": {"a": 1}, "0xDef": {"c": 3}}
	if _, ok := lookupCaseInsensitive(miss, "0xdead"); ok {
		t.Fatal("多键表中未命中不应返回值")
	}

	// 数据源偶尔键名不同但只返回一项：应兜底
	single := map[string]map[string]any{"whatever": {"b": 2}}
	if v, ok := lookupCaseInsensitive(single, "0xabc"); !ok || v["b"] != 2 {
		t.Fatal("单键应兜底返回")
	}

	// 多项且都不匹配：不得兜底（避免张冠李戴）
	multi := map[string]map[string]any{"k1": {"x": 1}, "k2": {"y": 2}}
	if _, ok := lookupCaseInsensitive(multi, "0xabc"); ok {
		t.Fatal("多项不匹配时不应兜底")
	}
	if _, ok := lookupCaseInsensitive(nil, "0xabc"); ok {
		t.Fatal("空表不应命中")
	}
}

func TestFlagValueAcrossTypeVariants(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"字符串 1", "1", true},
		{"字符串 true", "true", true},
		{"字符串 TRUE", "TRUE", true},
		{"字符串 0", "0", false},
		{"字符串 yes", "yes", false},
		{"数字 1", float64(1), true},
		{"数字 0", float64(0), false},
		{"数字 2", float64(2), false},
		{"nil", nil, false},
		{"未知类型", []any{1}, false},
	}
	for _, c := range cases {
		if got := flagValue(map[string]any{"f": c.in}, "f"); got != c.want {
			t.Fatalf("%s：flagValue=%v，期望 %v", c.name, got, c.want)
		}
	}
	if flagValue(map[string]any{}, "missing") {
		t.Fatal("缺失字段应为 false")
	}
}

func TestStrValueFormatsValues(t *testing.T) {
	m := map[string]any{"s": "text", "f": float64(1.5), "i": float64(5), "t": true, "fa": false, "n": nil}
	if got := strValue(m, "s"); got != "text" {
		t.Fatalf("字符串原样返回：%q", got)
	}
	if got := strValue(m, "f"); got != "1.5" {
		t.Fatalf("小数应格式化：%q", got)
	}
	if got := strValue(m, "i"); got != "5" {
		t.Fatalf("整数浮点不应带小数位：%q", got)
	}
	if got := strValue(m, "t"); got != "true" {
		t.Fatalf("布尔应转字符串：%q", got)
	}
	if got := strValue(m, "fa"); got != "false" {
		t.Fatalf("布尔 false 应转字符串：%q", got)
	}
	if got := strValue(m, "n"); got != "" {
		t.Fatalf("nil 应返回空串：%q", got)
	}
	if got := strValue(m, "missing"); got != "" {
		t.Fatalf("缺失字段应返回空串：%q", got)
	}
}

func TestFloatValueParsesStringsAndRejectsGarbage(t *testing.T) {
	m := map[string]any{"num": float64(12.5), "str": "  8.25  ", "bad": "n/a", "t": true, "f": false, "list": []any{1}}
	if got := floatValue(m, "num"); got != 12.5 {
		t.Fatalf("数字原样返回：%v", got)
	}
	if got := floatValue(m, "str"); got != 8.25 {
		t.Fatalf("字符串应去空白后解析：%v", got)
	}
	if got := floatValue(m, "bad"); got != 0 {
		t.Fatalf("非法字符串应返回 0 而非 panic：%v", got)
	}
	if got := floatValue(m, "t"); got != 1 {
		t.Fatalf("true 应为 1：%v", got)
	}
	if got := floatValue(m, "f"); got != 0 {
		t.Fatalf("false 应为 0：%v", got)
	}
	if got := floatValue(m, "list"); got != 0 {
		t.Fatalf("未知类型应返回 0：%v", got)
	}
	if got := floatValue(m, "missing"); got != 0 {
		t.Fatalf("缺失字段应返回 0：%v", got)
	}
}
