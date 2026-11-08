package server

import (
	"errors"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestRepeatTrackerBlocksThirdIdenticalCallAndCapsBlocks(t *testing.T) {
	rt := newRepeatTracker()
	input := []byte(`{"session_id":"s1","command":"whoami"}`)
	for i := 1; i <= 2; i++ {
		streak, block := rt.observe("exec", input)
		if block || streak != i {
			t.Fatalf("call %d: streak=%d block=%v", i, streak, block)
		}
	}
	for i := 3; i <= 4; i++ {
		streak, block := rt.observe("exec", input)
		if !block || streak != i {
			t.Fatalf("blocked call %d: streak=%d block=%v", i, streak, block)
		}
	}
	if streak, block := rt.observe("exec", input); block || streak != 5 {
		t.Fatalf("block cap should allow the fifth call: streak=%d block=%v", streak, block)
	}
}

func TestRepeatTrackerResetsOnChangedInputOrTool(t *testing.T) {
	rt := newRepeatTracker()
	for i := 0; i < 2; i++ {
		rt.observe("exec", []byte(`{"command":"whoami"}`))
	}
	if streak, block := rt.observe("exec", []byte(`{"command":"id"}`)); block || streak != 1 {
		t.Fatalf("changed input should reset: streak=%d block=%v", streak, block)
	}
	if streak, block := rt.observe("process_list", []byte(`{"command":"id"}`)); block || streak != 1 {
		t.Fatalf("changed tool should reset: streak=%d block=%v", streak, block)
	}
}

func TestCanonicalToolInputIgnoresJSONWhitespace(t *testing.T) {
	a := canonicalToolInput("exec", []byte("{\"session_id\":\"s1\",\"command\":\"whoami\"}"))
	b := canonicalToolInput("exec", []byte("{\n  \"session_id\": \"s1\",\n  \"command\": \"whoami\"\n}"))
	if a != b {
		t.Fatal("whitespace-only JSON changes should share one canonical hash")
	}
}

func TestIntentBudgetWatcherSteersOnceAtLimit(t *testing.T) {
	var messages []string
	watch := intentBudgetWatcher(intentBudgetConfig{Enabled: true, Tokens: 100}, func(msg string) error {
		messages = append(messages, msg)
		return nil
	}, "work#1 · #7")
	if watch == nil {
		t.Fatal("enabled budget returned nil watcher")
	}
	input, output := 40, 20
	watch(db.Activity{Kind: "usage", InputTokens: &input, OutputTokens: &output})
	if len(messages) != 0 {
		t.Fatal("steered below budget")
	}
	input, output = 90, 20
	watch(db.Activity{Kind: "usage", InputTokens: &input, OutputTokens: &output})
	watch(db.Activity{Kind: "usage", InputTokens: &input, OutputTokens: &output})
	if len(messages) != 1 {
		t.Fatalf("budget watcher should steer once, got %d", len(messages))
	}
}

func TestIntentBudgetWatcherDisabledOrSteerFailure(t *testing.T) {
	if got := intentBudgetWatcher(intentBudgetConfig{Enabled: false, Tokens: 100}, func(string) error { return nil }, "x"); got != nil {
		t.Fatal("disabled budget returned a watcher")
	}
	var calls int
	watch := intentBudgetWatcher(intentBudgetConfig{Enabled: true, Tokens: 1}, func(string) error {
		calls++
		return errors.New("not running")
	}, "x")
	input, output := 2, 0
	watch(db.Activity{Kind: "usage", InputTokens: &input, OutputTokens: &output})
	if calls != 1 {
		t.Fatalf("steer attempts=%d, want 1", calls)
	}
}

func TestClampIntentBudgetTokens(t *testing.T) {
	if got := clampIntentBudgetTokens(1); got != minIntentBudgetTokens {
		t.Fatalf("low budget = %d", got)
	}
	if got := clampIntentBudgetTokens(maxIntentBudgetTokens + 1); got != maxIntentBudgetTokens {
		t.Fatalf("high budget = %d", got)
	}
	if got := clampIntentBudgetTokens(1_500_000); got != 1_500_000 {
		t.Fatalf("valid budget changed: %d", got)
	}
}
