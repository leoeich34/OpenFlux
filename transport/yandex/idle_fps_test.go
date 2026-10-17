package yandex

import (
	"testing"
	"time"
)

func TestKeepaliveIntervalFastWhenRecentlyActive(t *testing.T) {
	fast := time.Second / time.Duration(publishFPS)
	// Just had data: full-rate interval.
	if got := keepaliveInterval(10 * time.Millisecond); got != fast {
		t.Errorf("recently-active interval = %v, want fast %v", got, fast)
	}
}

func TestKeepaliveIntervalIdleWhenQuiet(t *testing.T) {
	// Quiet longer than the linger: idle (slow) interval.
	idle := time.Second / time.Duration(idlePublishFPS)
	if got := keepaliveInterval(time.Duration(activeLingerMs+500) * time.Millisecond); got != idle {
		t.Errorf("quiet interval = %v, want idle %v", got, idle)
	}
}

func TestKeepaliveIntervalIdleDisabledStaysFast(t *testing.T) {
	old := idlePublishFPS
	idlePublishFPS = 0 // disabled -> never throttle
	defer func() { idlePublishFPS = old }()
	fast := time.Second / time.Duration(publishFPS)
	if got := keepaliveInterval(time.Hour); got != fast {
		t.Errorf("idle-disabled interval = %v, want fast %v", got, fast)
	}
}
