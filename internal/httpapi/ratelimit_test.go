package httpapi

import (
	"testing"
	"time"
)

func TestRateLimiterBurstThenDeny(t *testing.T) {
	l := &rateLimiter{buckets: map[string]*bucket{}, rps: 10, burst: 3}
	// burst of 3 should pass, the 4th in the same instant should be denied
	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed within burst", i+1)
		}
	}
	if l.allow("1.2.3.4") {
		t.Fatal("4th immediate request should be denied")
	}
	// a different IP has its own bucket
	if !l.allow("5.6.7.8") {
		t.Fatal("a different IP should not be rate-limited by another's usage")
	}
}

func TestRateLimiterRefills(t *testing.T) {
	l := &rateLimiter{buckets: map[string]*bucket{}, rps: 100, burst: 1}
	if !l.allow("ip") {
		t.Fatal("first request should pass")
	}
	if l.allow("ip") {
		t.Fatal("second immediate request should be denied")
	}
	time.Sleep(20 * time.Millisecond) // ~2 tokens refilled at 100 rps
	if !l.allow("ip") {
		t.Fatal("request after refill should pass")
	}
}

func TestClientIP(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4:5678":      "1.2.3.4",
		"[::1]:8080":        "::1",
		"no-port-weirdness": "no-port-weirdness",
	}
	for in, want := range cases {
		if got := clientIPFromAddr(in); got != want {
			t.Errorf("clientIP(%q) = %q, want %q", in, got, want)
		}
	}
}
