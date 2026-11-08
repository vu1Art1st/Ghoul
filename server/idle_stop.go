package server

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	idleStopRounds        = 3
	idleStopPreventRounds = 5
	idleStopMinElapsed    = 3 * time.Minute
)

var idleStopWriteTools = map[string]bool{
	"record_fact":    true,
	"insert_assets":  true,
	"report_finding": true,
	"save_evidence":  true,
}

const idleStopMessage = "【无增量收尾提醒】本意图已连续多轮没有写回任何事实、资产、漏洞或证据，" +
	"说明当前方向暂时没有新产出。请立即停止扩大探测范围：把已经确认的结论写回，" +
	"否定结果也可以记录为事实；写回后用一句话总结并结束本轮。"

type idleStopTracker struct {
	mu         sync.Mutex
	start      time.Time
	minElapsed time.Duration
	rounds     int
	lastWrites int64
	writes     atomic.Int64
	nudged     bool
	stopped    bool
	reason     string
}

func newIdleStopTracker() *idleStopTracker {
	return &idleStopTracker{start: time.Now(), minElapsed: idleStopMinElapsed}
}

func (s *idleStopTracker) noteWrite() {
	if s != nil {
		s.writes.Add(1)
	}
}

func (s *idleStopTracker) refreshLocked() bool {
	total := s.writes.Load()
	if total <= s.lastWrites {
		return false
	}
	s.lastWrites = total
	s.rounds = 0
	s.nudged = false
	s.stopped = false
	s.reason = ""
	return true
}

func (s *idleStopTracker) eligibleLocked() bool {
	if s.refreshLocked() {
		return false
	}
	return time.Since(s.start) >= s.minElapsed && s.rounds >= idleStopRounds
}

func (s *idleStopTracker) beforeTool(name string) (block bool, message string, cancel bool) {
	if s == nil || idleStopWriteTools[name] {
		return false, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.eligibleLocked() {
		return false, "", false
	}
	s.reason = fmt.Sprintf("连续 %d 轮无写回（累计写回 %d 条）", s.rounds, s.writes.Load())
	if !s.nudged {
		s.nudged = true
		return true, idleStopMessage, false
	}
	if s.rounds >= idleStopPreventRounds {
		s.stopped = true
		return true, idleStopMessage + "（已多次提示仍未写回，本轮就此结束）", true
	}
	return true, idleStopMessage + "（请立即写回并停止调用工具；继续空转将结束本轮）", false
}

func (s *idleStopTracker) advance(name string) {
	if s == nil || idleStopWriteTools[name] {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshLocked() {
		return
	}
	s.rounds++
}

func (s *idleStopTracker) checkStop() (prevent bool, blocking []string, reason string, cancel bool) {
	if s == nil {
		return false, nil, "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refreshLocked() {
		return false, nil, "", false
	}
	s.rounds++
	if time.Since(s.start) < s.minElapsed || s.rounds < idleStopRounds {
		return false, nil, "", false
	}
	s.reason = fmt.Sprintf("连续 %d 轮无写回（累计写回 %d 条）", s.rounds, s.writes.Load())
	if !s.nudged {
		s.nudged = true
		return false, []string{idleStopMessage}, "", false
	}
	if s.rounds < idleStopPreventRounds {
		return false, []string{idleStopMessage + "（请立即写回并停止调用工具；继续空转将结束本轮）"}, "", false
	}
	s.stopped = true
	return true, nil, s.reason, true
}

func (s *idleStopTracker) stoppedByIdle() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}
