package model

import "testing"

// TestAlertRule_ZeroDurationGeneralRule 回归 GHSA 级 DoS：Duration==0 的常规规则
// 曾在 fail*100/total 处触发整数除零 panic（total=duration=0）。该 panic 发生在
// 无 recover 的 checkStatus goroutine 中，一次即可使全站告警停摆，且任何能创建
// 告警规则的用户都可通过 Duration:0 触发。Check 现在跳过 duration<=0 的规则，
// 不再 panic。
func TestAlertRule_ZeroDurationGeneralRule(t *testing.T) {
	rule := &AlertRule{Rules: []*Rule{{Duration: 0}}}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("zero-duration rule must not panic, got: %v", r)
		}
	}()
	d, passed := rule.Check(repeat([]bool{false}, 5))
	assertEq(t, "ZeroDurationGeneral_d", 0, d)
	assertEq(t, "ZeroDurationGeneral_passed", false, passed)
}

// TestAlertRule_ZeroDurationMixedWithValidRule 确认被跳过的 0 时长规则不会污染
// 同一 alert 内其它有效规则的判定——跳过用 continue 而非 boundCheck，否则会把
// hasPassedRule 置真而连带跳过同组有效规则。
func TestAlertRule_ZeroDurationMixedWithValidRule(t *testing.T) {
	rule := &AlertRule{Rules: []*Rule{
		{Duration: 0},  // 无意义规则，应被跳过
		{Duration: 10}, // 有效常规规则，全部采样失败 → 应触发告警
	}}
	points := repeat([]bool{false, false}, 10)
	d, passed := rule.Check(points)
	assertEq(t, "MixedValid_d", 10, d)
	assertEq(t, "MixedValid_passed", false, passed)
}

// TestAlertRule_RetentionWindow 锁定采样保留窗口只依赖规则定义：常规/离线规则
// 保留 Duration 个采样（Check 读取 points[len-Duration:]），周期流量规则只看最后
// 一个采样故保留 1，全部 Duration<=0 时窗口为 0（调用方据此清空采样，防止内存
// 无限增长）。离线规则保留 Duration（非 1）是关键，否则窗口攒不够、离线告警永不
// 触发。
func TestAlertRule_RetentionWindow(t *testing.T) {
	cases := []struct {
		msg  string
		rule *AlertRule
		want int
	}{
		{"general retains Duration", &AlertRule{Rules: []*Rule{{Duration: 7}}}, 7},
		{"offline retains Duration", &AlertRule{Rules: []*Rule{{Type: "offline", Duration: 10}}}, 10},
		{"transfer cycle retains 1", &AlertRule{Rules: []*Rule{{Type: "transfer_all_cycle", Duration: 30}}}, 1},
		{"all zero clears", &AlertRule{Rules: []*Rule{{Duration: 0}}}, 0},
		{"max across mixed rules", &AlertRule{Rules: []*Rule{
			{Duration: 3},
			{Type: "offline", Duration: 12},
			{Type: "transfer_all_cycle"},
		}}, 12},
	}
	for _, c := range cases {
		assertEq(t, c.msg, c.want, c.rule.RetentionWindow())
	}
}
