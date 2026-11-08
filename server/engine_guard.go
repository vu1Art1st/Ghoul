package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Autumn-27/artex/db"
)

const (
	settingIntentBudgetEnabled = "intent_budget_enabled"
	settingIntentBudgetTokens  = "intent_budget_tokens"

	defaultIntentBudgetEnabled = true
	defaultIntentBudgetTokens  = int64(3_000_000)
	minIntentBudgetTokens      = int64(500_000)
	maxIntentBudgetTokens      = int64(20_000_000)
)

type intentBudgetConfig struct {
	Enabled bool
	Tokens  int64
}

// intentBudgetFromPG reads the soft budget settings. A database/settings error
// falls back to the conservative defaults rather than turning the guard into a
// hard failure.
func intentBudgetFromPG(pg *db.DB) intentBudgetConfig {
	cfg := intentBudgetConfig{Enabled: defaultIntentBudgetEnabled, Tokens: defaultIntentBudgetTokens}
	if pg == nil {
		return cfg
	}
	cfg.Enabled = pg.GetBool(settingIntentBudgetEnabled, defaultIntentBudgetEnabled)
	if raw, ok, err := pg.GetSetting(settingIntentBudgetTokens); err == nil && ok {
		if n, parseErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); parseErr == nil {
			cfg.Tokens = clampIntentBudgetTokens(n)
		}
	}
	return cfg
}

func clampIntentBudgetTokens(tokens int64) int64 {
	if tokens < minIntentBudgetTokens {
		return minIntentBudgetTokens
	}
	if tokens > maxIntentBudgetTokens {
		return maxIntentBudgetTokens
	}
	return tokens
}

func budgetNudgeText(limit int64) string {
	return fmt.Sprintf(
		"【预算提醒】本意图的累计 token 消耗已达到预算上限（约 %d）。立即停止扩大探索范围："+
			"把已经确认的结论用 record_fact 写回，确认漏洞则用 report_finding；"+
			"然后直接给出结束答复。不要再开启新的探测动作，未验证的方向留给规划者另派。",
		limit,
	)
}

// intentBudgetWatcher turns cumulative usage activities into a one-shot steer
// reminder. It is deliberately soft: the run is not killed, so confirmed facts
// can still be written back before the model ends the intent.
func intentBudgetWatcher(cfg intentBudgetConfig, steer func(string) error, label string) func(db.Activity) {
	if !cfg.Enabled || cfg.Tokens <= 0 || steer == nil {
		return nil
	}
	var nudged atomic.Bool
	return func(a db.Activity) {
		if a.Kind != "usage" || a.InputTokens == nil || nudged.Load() {
			return
		}
		output := 0
		if a.OutputTokens != nil {
			output = *a.OutputTokens
		}
		total := int64(*a.InputTokens) + int64(output)
		if total < cfg.Tokens {
			return
		}
		if !nudged.CompareAndSwap(false, true) {
			return
		}
		if err := steer(budgetNudgeText(cfg.Tokens)); err != nil {
			log.Printf("[budget %s] 预算提醒入队失败: %v", label, err)
			return
		}
		log.Printf("[budget %s] 意图 token 达到预算上限 %d（累计 %d），已注入收尾提醒", label, cfg.Tokens, total)
	}
}

// repeatTracker detects the exact same tool call repeated back-to-back. It
// blocks only after the third identical call and caps the number of blocks so a
// confused model can always make progress again with a changed input.
type repeatTracker struct {
	mu       sync.Mutex
	blockCap int
	name     string
	hash     string
	streak   int
	blocked  int
}

func newRepeatTracker() *repeatTracker {
	return &repeatTracker{blockCap: 2}
}

func canonicalToolInput(name string, input []byte) string {
	var compact bytes.Buffer
	if err := json.Compact(&compact, input); err != nil {
		compact.Reset()
		compact.WriteString(string(input))
	}
	sum := sha256.Sum256(append([]byte(name+"|"), compact.Bytes()...))
	return hex.EncodeToString(sum[:])
}

func (rt *repeatTracker) observe(name string, input []byte) (streak int, block bool) {
	if rt == nil {
		return 0, false
	}
	hash := canonicalToolInput(name, input)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.name == name && rt.hash == hash {
		rt.streak++
	} else {
		rt.name = name
		rt.hash = hash
		rt.streak = 1
		rt.blocked = 0
	}
	if rt.streak >= 3 && rt.blocked < rt.blockCap {
		rt.blocked++
		return rt.streak, true
	}
	return rt.streak, false
}

func repeatBlockText(streak int) string {
	return fmt.Sprintf(
		"【重复调用提醒】这个工具调用与你最近的调用完全相同（连续第 %d 次），本次未执行。"+
			"同样的输入只会得到同样的输出——请改变目标、参数或方法；"+
			"如果当前手段已经到头，请把结论用 record_fact 写回（否定结论同样有效）。",
		streak,
	)
}
