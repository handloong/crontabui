package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// maxFailures 是单个来源在窗口内允许的最大登录失败次数。
	maxFailures = 10
	// failureWindow 是统计登录失败的滑动窗口。
	failureWindow = 5 * time.Minute
	// blockDuration 是触发限流后的封禁时长。
	blockDuration = 5 * time.Minute
)

type attempt struct {
	count   int
	first   time.Time
	blocked time.Time
}

// Manager 持有 Token 摘要、内存会话与登录失败计数。所有状态仅存内存，不落盘。
type Manager struct {
	tokenHash [32]byte
	ttl       time.Duration
	// Now 可注入的时钟，便于测试。
	Now func() time.Time

	mu       sync.Mutex
	sessions map[string]time.Time
	failures map[string]*attempt
}

// New 创建 Manager。ttl 为会话滑动有效期。
func New(token string, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &Manager{
		tokenHash: sha256.Sum256([]byte(token)),
		ttl:       ttl,
		Now:       time.Now,
		sessions:  make(map[string]time.Time),
		failures:  make(map[string]*attempt),
	}
}

// VerifyToken 以常量时间比较 Token，避免长度与内容的时序泄漏。
func (m *Manager) VerifyToken(token string) bool {
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], m.tokenHash[:]) == 1
}

// CreateSession 生成会话 ID 并登记过期时间。
func (m *Manager) CreateSession() (id string, expires time.Time) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		raw = []byte(time.Now().UTC().Format(time.RFC3339Nano))
	}
	id = hex.EncodeToString(raw)
	expires = m.Now().Add(m.ttl)
	m.mu.Lock()
	m.sessions[id] = expires
	m.mu.Unlock()
	return id, expires
}

// Validate 校验会话并做滑动续期。
func (m *Manager) Validate(id string) bool {
	if id == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	expires, ok := m.sessions[id]
	if !ok {
		return false
	}
	now := m.Now()
	if !now.Before(expires) {
		delete(m.sessions, id)
		return false
	}
	m.sessions[id] = now.Add(m.ttl)
	return true
}

// Destroy 使会话立即失效。
func (m *Manager) Destroy(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// SessionCount 返回当前活跃会话数。
func (m *Manager) SessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// AllowLogin 判断来源 IP 当前是否允许尝试登录。
func (m *Manager) AllowLogin(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.failures[ip]
	if !ok {
		return true
	}
	now := m.Now()
	if !a.blocked.IsZero() && now.Before(a.blocked) {
		return false
	}
	if now.Sub(a.first) > failureWindow {
		delete(m.failures, ip)
		return true
	}
	return a.count < maxFailures
}

// RecordFailure 记录一次登录失败，达到阈值后封禁该来源。
func (m *Manager) RecordFailure(ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	a, ok := m.failures[ip]
	if !ok || now.Sub(a.first) > failureWindow {
		m.failures[ip] = &attempt{count: 1, first: now}
		return
	}
	a.count++
	if a.count >= maxFailures {
		a.blocked = now.Add(blockDuration)
	}
}

// ResetFailures 在登录成功后清除该来源的失败记录。
func (m *Manager) ResetFailures(ip string) {
	m.mu.Lock()
	delete(m.failures, ip)
	m.mu.Unlock()
}

// Cleanup 清理过期会话与陈旧失败记录，由后台定时调用以限制内存占用。
func (m *Manager) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	for id, expires := range m.sessions {
		if !now.Before(expires) {
			delete(m.sessions, id)
		}
	}
	for ip, a := range m.failures {
		stale := now.Sub(a.first) > failureWindow
		unblocked := a.blocked.IsZero() || !now.Before(a.blocked)
		if stale && unblocked {
			delete(m.failures, ip)
		}
	}
}
