package server

import (
	"testing"
	"time"
)

func testIdleTracker() *idleStopTracker {
	tracker := newIdleStopTracker()
	tracker.start = time.Now().Add(-idleStopMinElapsed - time.Second)
	return tracker
}

func TestIdleStopNudgesThenCancelsAfterNoWrites(t *testing.T) {
	tracker := testIdleTracker()
	for i := 0; i < idleStopRounds; i++ {
		tracker.advance("exec")
	}
	block, msg, cancel := tracker.beforeTool("exec")
	if !block || cancel || msg == "" {
		t.Fatalf("first idle threshold: block=%v cancel=%v msg=%q", block, cancel, msg)
	}
	for tracker.rounds < idleStopPreventRounds {
		tracker.advance("exec")
	}
	block, msg, cancel = tracker.beforeTool("exec")
	if !block || !cancel || msg == "" {
		t.Fatalf("hard idle threshold: block=%v cancel=%v msg=%q", block, cancel, msg)
	}
}

func TestIdleStopWriteResetsCounterAndIsNeverBlocked(t *testing.T) {
	tracker := testIdleTracker()
	for i := 0; i < idleStopRounds; i++ {
		tracker.advance("exec")
	}
	tracker.noteWrite()
	if block, _, cancel := tracker.beforeTool("exec"); block || cancel {
		t.Fatal("write should reset the idle counter")
	}
	if block, _, cancel := tracker.beforeTool("record_fact"); block || cancel {
		t.Fatal("write tools must always be allowed")
	}
	tracker.advance("record_fact")
	if tracker.rounds != 0 {
		t.Fatalf("write tool advanced idle rounds to %d", tracker.rounds)
	}
}

func TestIdleStopStopHookNudgesThenPrevents(t *testing.T) {
	tracker := testIdleTracker()
	tracker.rounds = idleStopRounds - 1
	prevent, blocking, _, cancel := tracker.checkStop()
	if prevent || cancel || len(blocking) != 1 {
		t.Fatalf("first Stop nudge: prevent=%v cancel=%v blocking=%v", prevent, cancel, blocking)
	}
	for tracker.rounds < idleStopPreventRounds-1 {
		tracker.rounds++
	}
	prevent, blocking, reason, cancel := tracker.checkStop()
	if !prevent || !cancel || len(blocking) != 0 || reason == "" {
		t.Fatalf("Stop hard stop: prevent=%v cancel=%v blocking=%v reason=%q", prevent, cancel, blocking, reason)
	}
}
