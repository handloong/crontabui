package auth

import (
	"testing"
	"time"
)

func newTestManager(t *testing.T) (*Manager, *time.Time) {
	t.Helper()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := &clock
	m := New("s3cr3t-token-value", 12*time.Hour)
	m.Now = func() time.Time { return *now }
	return m, now
}

func TestVerifyToken(t *testing.T) {
	m, _ := newTestManager(t)
	if !m.VerifyToken("s3cr3t-token-value") {
		t.Error("correct token rejected")
	}
	for _, bad := range []string{"", "x", "s3cr3t-token-valuf", "s3cr3t-token-value-longer", "S3CR3T-TOKEN-VALUE"} {
		if m.VerifyToken(bad) {
			t.Errorf("token %q accepted, want rejected", bad)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	m, now := newTestManager(t)

	id, exp := m.CreateSession()
	if len(id) != 64 {
		t.Fatalf("session id = %q, want 64 hex chars", id)
	}
	if !exp.After(*now) {
		t.Error("session expiry should be in the future")
	}
	if !m.Validate(id) {
		t.Error("fresh session should validate")
	}
	if m.Validate("deadbeef") {
		t.Error("unknown session should not validate")
	}

	m.Destroy(id)
	if m.Validate(id) {
		t.Error("destroyed session should not validate")
	}
}

func TestSessionSlidingExpiry(t *testing.T) {
	m, now := newTestManager(t)
	id, _ := m.CreateSession()

	*now = now.Add(11 * time.Hour)
	if !m.Validate(id) {
		t.Fatal("session should still be alive at 11h")
	}
	// touched -> expiry slides forward
	*now = now.Add(11 * time.Hour)
	if !m.Validate(id) {
		t.Fatal("session should slide past the original 12h deadline")
	}

	*now = now.Add(13 * time.Hour)
	if m.Validate(id) {
		t.Error("session should expire after idle ttl")
	}
}

func TestSessionCount(t *testing.T) {
	m, _ := newTestManager(t)
	a, _ := m.CreateSession()
	b, _ := m.CreateSession()
	if n := m.SessionCount(); n != 2 {
		t.Fatalf("SessionCount = %d, want 2", n)
	}
	m.Destroy(a)
	m.Destroy(b)
	if n := m.SessionCount(); n != 0 {
		t.Errorf("SessionCount after destroy = %d, want 0", n)
	}
}

func TestLoginRateLimit(t *testing.T) {
	m, now := newTestManager(t)
	const ip = "10.0.0.1"

	for i := 0; i < maxFailures-1; i++ {
		m.RecordFailure(ip)
		if !m.AllowLogin(ip) {
			t.Fatalf("blocked too early after %d failures", i+1)
		}
	}
	m.RecordFailure(ip)
	if m.AllowLogin(ip) {
		t.Fatal("should be blocked after maxFailures failures")
	}
	// a different IP is unaffected
	if !m.AllowLogin("10.0.0.2") {
		t.Error("other IP should not be blocked")
	}

	*now = now.Add(failureWindow + time.Minute)
	if !m.AllowLogin(ip) {
		t.Error("block should lift after the window")
	}
}

func TestSuccessfulLoginClearsFailures(t *testing.T) {
	m, _ := newTestManager(t)
	const ip = "10.0.0.9"
	m.RecordFailure(ip)
	m.ResetFailures(ip)
	if !m.AllowLogin(ip) {
		t.Error("AllowLogin should be true after reset")
	}
	for i := 0; i < maxFailures; i++ {
		m.RecordFailure(ip)
	}
	if m.AllowLogin(ip) {
		t.Error("should be blocked after re-accumulating failures")
	}
}

func TestCleanupDropsStaleState(t *testing.T) {
	m, now := newTestManager(t)
	id, _ := m.CreateSession()
	m.RecordFailure("10.1.1.1")

	*now = now.Add(72 * time.Hour)
	m.Cleanup()
	if n := m.SessionCount(); n != 0 {
		t.Errorf("SessionCount = %d after cleanup, want 0", n)
	}
	if m.Validate(id) {
		t.Error("expired session survived cleanup")
	}
	if !m.AllowLogin("10.1.1.1") {
		t.Error("stale failure record survived cleanup")
	}
}
