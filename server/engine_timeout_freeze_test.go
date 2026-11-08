package server

import (
	"testing"
	"time"
)

func TestDetectFreeze(t *testing.T) {
	cases := []struct {
		name    string
		planned time.Duration
		actual  time.Duration
		want    time.Duration
	}{
		{"normal poll", 2 * time.Second, 2*time.Second + 50*time.Millisecond, 0},
		{"normal nap", 30 * time.Second, 30*time.Second + 500*time.Millisecond, 0},
		{"first wake", 0, 10 * time.Millisecond, 0},
		{"freeze 65s", 2 * time.Second, 67 * time.Second, 65 * time.Second},
		{"freeze 7h", 30 * time.Second, 7*time.Hour + 30*time.Second, 7 * time.Hour},
		{"below threshold", 30 * time.Second, 30*time.Second + freezeDetectThreshold - time.Second, 0},
		{"clock rollback", 30 * time.Second, 5 * time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectFreeze(tc.planned, tc.actual); got != tc.want {
				t.Fatalf("detectFreeze(%v, %v) = %v, want %v", tc.planned, tc.actual, got, tc.want)
			}
		})
	}
}

func TestDetectFreezeDoesNotAccumulateSmallJumps(t *testing.T) {
	var shift time.Duration
	for i := 0; i < 5; i++ {
		shift += detectFreeze(2*time.Second, 32*time.Second)
	}
	if shift != 0 {
		t.Fatalf("small jumps accumulated to %v", shift)
	}
}
