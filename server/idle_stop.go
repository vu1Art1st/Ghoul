package server

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	idleStopRounds          = 12
	idleStopPreventRounds   = 20
	idleStopMinElapsed      = 8 * time.Minute
	idleStopAdvisoryRounds  = 8
	idleStopAdvisoryElapsed = 5 * time.Minute
)

const settingWorkerIdleStopMode = "worker_idle_stop_mode"

type idleStopMode string

const (
	idleStopModeOff      idleStopMode = "off"
	idleStopModeAdvisory idleStopMode = "advisory"
	idleStopModeEnforce  idleStopMode = "enforce"
)

type idleStopSettings interface {
	GetSetting(string) (string, bool, error)
}

func workerIdleStopModeFromPG(pg idleStopSettings) idleStopMode {
	if pg == nil {
		return idleStopModeOff
	}
	raw, ok, err := pg.GetSetting(settingWorkerIdleStopMode)
	if err != nil || !ok {
		return idleStopModeOff
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "advisory":
		return idleStopModeAdvisory
	case "enforce", "enabled", "true", "1":
		return idleStopModeEnforce
	default:
		return idleStopModeOff
	}
}

var idleStopWriteTools = map[string]bool{
	"record_fact":                true,
	"insert_assets":              true,
	"report_finding":             true,
	"save_evidence":              true,
	"save_finding_screenshot":    true,
	"capture_finding_screenshot": true,
	"bind_finding_traffic":       true,
}

const idleStopMessage = "【无增量收尾提醒】本意图已连续多轮没有写回任何事实、资产、漏洞或证据，" +
	"说明当前方向暂时没有新产出。请立即停止扩大探测范围：把已经确认的结论写回，" +
	"否定结果也可以记录为事实；写回后用一句话总结并结束本轮。"

type idleStopTracker struct {
	mu         sync.Mutex
	mode       idleStopMode
	start      time.Time
	minElapsed time.Duration
	rounds     int
	nudgeRound int
	lastWrites int64
	writes     atomic.Int64
	nudged     bool
	stopped    bool
	reason     string
}

func newIdleStopTracker() *idleStopTracker {
	return newIdleStopTrackerForMode(idleStopModeEnforce)
}

func newIdleStopTrackerForMode(mode idleStopMode) *idleStopTracker {
	switch mode {
	case idleStopModeAdvisory:
		return &idleStopTracker{mode: mode, start: time.Now(), minElapsed: idleStopAdvisoryElapsed}
	case idleStopModeEnforce:
		return &idleStopTracker{mode: mode, start: time.Now(), minElapsed: idleStopMinElapsed}
	default:
		return nil
	}
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
	// A write is a new phase boundary. Screenshot collection, report handoff and
	// traffic binding often happen immediately after the finding was recorded;
	// they deserve a fresh grace window instead of inheriting the exploration
	// phase's elapsed time.
	s.start = time.Now()
	s.rounds = 0
	s.nudgeRound = 0
	s.nudged = false
	s.stopped = false
	s.reason = ""
	return true
}

func (s *idleStopTracker) nudgeRounds() int {
	if s.mode == idleStopModeAdvisory {
		return idleStopAdvisoryRounds
	}
	return idleStopRounds
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
	if time.Since(s.start) < s.minElapsed || s.rounds < s.nudgeRounds() {
		return false, nil, "", false
	}
	s.reason = fmt.Sprintf("连续 %d 轮无写回（累计写回 %d 条）", s.rounds, s.writes.Load())
	if !s.nudged {
		s.nudged = true
		s.nudgeRound = s.rounds
		return false, []string{idleStopMessage}, "", false
	}
	if s.mode == idleStopModeAdvisory {
		return false, nil, "", false
	}
	// A nudge is delivered at the Stop boundary, so the model receives it only
	// after the current assistant turn has ended. Never cancel in that same turn.
	if s.nudgeRound == 0 || s.rounds <= s.nudgeRound || s.rounds < idleStopPreventRounds {
		return false, []string{idleStopMessage + "（请立即写回并停止扩大范围；继续空转将结束本轮）"}, "", false
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
