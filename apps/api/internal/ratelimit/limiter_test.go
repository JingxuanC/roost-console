package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterWindow(t *testing.T) {
	l := New(2, time.Minute)
	now := time.Unix(1000, 0)
	if ok, _ := l.Allow("a", now); !ok {
		t.Fatal("first rejected")
	}
	if ok, _ := l.Allow("a", now); !ok {
		t.Fatal("second rejected")
	}
	if ok, wait := l.Allow("a", now); ok || wait <= 0 {
		t.Fatalf("third allowed or wait=%v", wait)
	}
	if ok, _ := l.Allow("b", now); !ok {
		t.Fatal("other key rejected")
	}
	if ok, _ := l.Allow("a", now.Add(61*time.Second)); !ok {
		t.Fatal("new window rejected")
	}
}
