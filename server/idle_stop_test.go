package server

import (
	"errors"
	"testing"
	"time"
)

type idleSettingsFixture struct {
	value string
	ok    bool
	err   error
}

func (f idleSettingsFixture) GetSetting(string) (string, bool, error) {
	return f.value, f.ok, f.err
}

func testIdleTracker() *idleStopTracker {
	tracker := newIdleStopTracker()
	tracker.start = time.Now().Add(-idleStopMinElapsed - time.Second)
	return tracker
}

func TestWorkerIdleStopDefaultsOffAndSupportsExplicitModes(t *testing.T) {
	cases := []struct {
		name string
		pg   idleStopSettings
		want idleStopMode
	}{
		{name: "nil defaults off", pg: nil, want: idleStopModeOff},
		{name: "missing defaults off", pg: idleSettingsFixture{}, want: idleStopModeOff},
		{name: "read error defaults off", pg: idleSettingsFixture{err: errors.New("db unavailable")}, want: idleStopModeOff},
		{name: "explicit off", pg: idleSettingsFixture{value: "off", ok: true}, want: idleStopModeOff},
		{name: "advisory", pg: idleSettingsFixture{value: "advisory", ok: true}, want: idleStopModeAdvisory},
		{name: "enforce", pg: idleSettingsFixture{value: "enforce", ok: true}, want: idleStopModeEnforce},
		{name: "legacy enabled maps to enforce", pg: idleSettingsFixture{value: "true", ok: true}, want: idleStopModeEnforce},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := workerIdleStopModeFromPG(tc.pg); got != tc.want {
				t.Fatalf("mode=%q want=%q", got, tc.want)
			}
		})
	}
	if tracker := newIdleStopTrackerForMode(idleStopModeOff); tracker != nil {
		t.Fatal("off mode must not create an idle-stop tracker")
	}
}

func TestIdleStopNudgesThenCancelsOnlyAfterAnotherTurn(t *testing.T) {
	tracker := testIdleTracker()
	tracker.rounds = idleStopRounds - 1
	prevent, blocking, _, cancel := tracker.checkStop()
	if prevent || cancel || len(blocking) != 1 {
		t.Fatalf("first Stop nudge: prevent=%v cancel=%v blocking=%v", prevent, cancel, blocking)
	}

	prevent, _, _, cancel = tracker.checkStop()
	if prevent || cancel {
		t.Fatal("idle stop must not cancel in the next Stop callback without giving the nudge a turn")
	}

	tracker.rounds = idleStopPreventRounds - 1
	prevent, blocking, reason, cancel := tracker.checkStop()
	if !prevent || !cancel || len(blocking) != 0 || reason == "" {
		t.Fatalf("hard threshold: prevent=%v cancel=%v blocking=%v reason=%q", prevent, cancel, blocking, reason)
	}
}

func TestIdleStopWriteResetsCounterAndGraceWindow(t *testing.T) {
	tracker := testIdleTracker()
	tracker.rounds = idleStopRounds - 1
	beforeReset := tracker.start
	tracker.noteWrite()
	prevent, blocking, _, cancel := tracker.checkStop()
	if prevent || cancel || len(blocking) != 0 {
		t.Fatal("write should reset the idle counter")
	}
	if !tracker.start.After(beforeReset) {
		t.Fatal("write should start a fresh grace window")
	}
	if tracker.rounds != 0 {
		t.Fatalf("a write resets the current idle window, got rounds=%d", tracker.rounds)
	}
}

func TestIdleStopTreatsScreenshotToolsAsProgress(t *testing.T) {
	for _, tool := range []string{"save_finding_screenshot", "capture_finding_screenshot", "bind_finding_traffic"} {
		if !idleStopWriteTools[tool] {
			t.Fatalf("%s must count as evidence-phase progress", tool)
		}
	}
}

func TestIdleStopAdvisoryNeverCancels(t *testing.T) {
	tracker := newIdleStopTrackerForMode(idleStopModeAdvisory)
	tracker.start = time.Now().Add(-idleStopAdvisoryElapsed - time.Second)
	tracker.rounds = idleStopAdvisoryRounds - 1
	prevent, blocking, _, cancel := tracker.checkStop()
	if prevent || cancel || len(blocking) != 1 {
		t.Fatalf("advisory nudge: prevent=%v cancel=%v blocking=%v", prevent, cancel, blocking)
	}
	for i := 0; i < idleStopPreventRounds+5; i++ {
		prevent, _, _, cancel = tracker.checkStop()
		if prevent || cancel {
			t.Fatal("advisory mode must never cancel a Work")
		}
	}
}

func TestTask221StyleFifteenTurnExplorationContinuesByDefault(t *testing.T) {
	mode := workerIdleStopModeFromPG(idleSettingsFixture{})
	hooks := steerHooks{
		drain:  func() (string, bool) { return "", false },
		nudges: nil,
		limit:  0,
		label:  "worker-1 · #3054",
		idle:   newIdleStopTrackerForMode(mode),
		cancel: func(error) { t.Fatal("default ARTEX 0.3.15 policy must not cancel the Work") },
	}
	for turn := 1; turn <= 15; turn++ {
		for call := 0; call < 2; call++ {
			block, message, _ := hooks.PreToolUse(t.Context(), "Bash", []byte(`{"command":"python3 run3.py"}`))
			if block {
				t.Fatalf("turn %d tool %d was blocked: %s", turn, call, message)
			}
			hooks.PostToolUse(t.Context(), "Bash", nil, nil, false)
		}
		prevent, blocking, _ := hooks.Stop(t.Context(), nil)
		if prevent || len(blocking) > 0 {
			t.Fatalf("turn %d stopped before the 1200-second wall-clock budget: prevent=%v blocking=%v", turn, prevent, blocking)
		}
	}
}

func TestIdleStopEnforceNeverCancelsInsideTheNudgeBatch(t *testing.T) {
	tracker := testIdleTracker()
	tracker.rounds = idleStopPreventRounds - 1
	tracker.nudged = true
	tracker.nudgeRound = idleStopRounds
	cancelled := false
	hooks := steerHooks{
		drain:  func() (string, bool) { return "", false },
		nudges: nil,
		limit:  0,
		label:  "worker-1 · #3054",
		idle:   tracker,
		cancel: func(error) { cancelled = true },
	}
	for call := 0; call < 2; call++ {
		if block, message, _ := hooks.PreToolUse(t.Context(), "knowledge_search", []byte(`{"q":"shiftPayload"}`)); block {
			t.Fatalf("tool %d should not be blocked from PreToolUse: %s", call, message)
		}
	}
	if cancelled {
		t.Fatal("enforce mode must never cancel while a tool batch is still executing")
	}
	prevent, _, _, cancel := tracker.checkStop()
	if !prevent || !cancel {
		t.Fatalf("enforce mode should cancel only at the next Stop boundary: prevent=%v cancel=%v", prevent, cancel)
	}
}
