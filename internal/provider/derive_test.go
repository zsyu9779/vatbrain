package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeriveWriteEvent_UserConfirmed_Chinese(t *testing.T) {
	// §5: 显式记忆指令 → UserConfirmed=true
	e := DeriveWriteEvent("记住：软路由 OpenClash 覆写脚本用 Ruby YAML 解析，不要用文本 gsub", "")
	assert.True(t, e.UserConfirmed)
	assert.False(t, e.IsCorrection)
	assert.Contains(t, e.Summary, "软路由 OpenClash 覆写脚本")

	e2 := DeriveWriteEvent("记一下，ClawFeed 推送必须用 clawfeed-push-v3.py", "")
	assert.True(t, e2.UserConfirmed)
}

func TestDeriveWriteEvent_UserConfirmed_English(t *testing.T) {
	e := DeriveWriteEvent("Remember this: miniMax max_tokens must be 8000", "")
	assert.True(t, e.UserConfirmed)
}

func TestDeriveWriteEvent_Correction_Chinese(t *testing.T) {
	// §5: 短消息 + 纠正动词 → IsCorrection=true（prediction-error 信号）
	e := DeriveWriteEvent("不对，evaluator 输出字段是 total_score 不是 overall_score", "")
	assert.True(t, e.IsCorrection)
	assert.False(t, e.UserConfirmed)
	assert.Contains(t, e.Summary, "total_score")

	e2 := DeriveWriteEvent("别用 clawfeed-push-feishu.py，改用 v3.py", "")
	assert.True(t, e2.IsCorrection)

	e3 := DeriveWriteEvent("这个不对，应该是配置在 feedpush.conf 而不是 .env", "")
	assert.True(t, e3.IsCorrection)
}

func TestDeriveWriteEvent_Correction_English(t *testing.T) {
	e := DeriveWriteEvent("Actually it's total_score not overall_score", "")
	assert.True(t, e.IsCorrection)
	e2 := DeriveWriteEvent("Don't use that script, use the other one", "")
	assert.True(t, e2.IsCorrection)
}

func TestDeriveWriteEvent_Correction_LongMessageNotCorrection(t *testing.T) {
	// §5: 长消息不命中纠正规则——避免把正常叙述当纠错
	long := "这是我今天在调试软路由时遇到的一个问题，整个过程比较长……"
	for len([]rune(long)) < 200 {
		long += "补充细节。"
	}
	assert.False(t, detectCorrection(long))
}

func TestDeriveWriteEvent_NormalTurn_NoSignal(t *testing.T) {
	e := DeriveWriteEvent("继续排查 ClawFeed 播报没发出去的问题", "")
	assert.False(t, e.UserConfirmed)
	assert.False(t, e.IsCorrection)
}

func TestDeriveWriteEvent_Summary_Truncated(t *testing.T) {
	long := ""
	for i := 0; i < maxSummaryRunes*2; i++ {
		long += "汉"
	}
	e := DeriveWriteEvent(long, "")
	assert.LessOrEqual(t, len([]rune(e.Summary)), maxSummaryRunes)
}

func TestDeriveWriteEvent_Summary_StripsTrivialPrefix(t *testing.T) {
	e := DeriveWriteEvent("继续：检查 openclash 缓存是否污染", "")
	assert.Equal(t, "检查 openclash 缓存是否污染", e.Summary)
}
