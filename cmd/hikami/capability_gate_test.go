package main

import (
	"sync"
	"testing"

	hzruntime "hikami-go/internal/runtime"
)

// stubRuntimeStatusSource 是 runtimeStatusSource 的测试桩:返回可变的最新状态
// (模拟 server 代际刷新)。
type stubRuntimeStatusSource struct {
	mu     sync.RWMutex
	status *hzruntime.Status
}

func (s *stubRuntimeStatusSource) CurrentRuntimeStatus() *hzruntime.Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *stubRuntimeStatusSource) set(st *hzruntime.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
}

func caps(asr, publish, webdav bool) hzruntime.Capabilities {
	return hzruntime.Capabilities{ASRSubmit: asr, PublishOpus: publish, WebDAVUpload: webdav}
}

// TestAutoChainGateFallsBackToStartupSnapshot M14:server 未 attach 时回退启动
// Probe 快照(gate 语义与旧 runtimeStatus.Capabilities 读取等价)。
func TestAutoChainGateFallsBackToStartupSnapshot(t *testing.T) {
	startup := &hzruntime.Status{Capabilities: caps(true, false, true)}
	gate := newAutoChainCapabilityGate(startup)

	if !gate.enabled(func(c hzruntime.Capabilities) bool { return c.ASRSubmit }) {
		t.Fatal("startup snapshot ASRSubmit=true 应放行")
	}
	if gate.enabled(func(c hzruntime.Capabilities) bool { return c.PublishOpus }) {
		t.Fatal("startup snapshot PublishOpus=false 应拦截")
	}
}

// TestAutoChainGateAttachedServerWins M14:attach 后读 server 最新状态;
// server 状态中途翻转(用户补齐配置后代际刷新)gate 跟随翻转,
// 不再被启动快照卡死——这是本项修复的核心回归。
func TestAutoChainGateAttachedServerWins(t *testing.T) {
	startup := &hzruntime.Status{Capabilities: caps(false, false, false)}
	gate := newAutoChainCapabilityGate(startup)

	src := &stubRuntimeStatusSource{status: &hzruntime.Status{Capabilities: caps(false, false, false)}}
	gate.attach(src)

	asr := func(c hzruntime.Capabilities) bool { return c.ASRSubmit }
	if gate.enabled(asr) {
		t.Fatal("server 当前 ASRSubmit=false 应拦截")
	}
	// 用户中途补齐配置 → server 刷新 → gate 放行(启动快照仍是 false)。
	src.set(&hzruntime.Status{Capabilities: caps(true, false, false)})
	if !gate.enabled(asr) {
		t.Fatal("server 刷新后 ASRSubmit=true 应放行(启动快照 false 不应再卡死)")
	}
}

// TestAutoChainGateServerBlocksDespiteStartupSnapshot M14 审核 Minor:钉死
// 「server 非 nil 状态优先于 fallback」的阻断方向——启动快照 true 但 server 刷新为
// false 时必须拦截,防止未来分支顺序被改坏(先查 fallback)时无测试可抓。
func TestAutoChainGateServerBlocksDespiteStartupSnapshot(t *testing.T) {
	startup := &hzruntime.Status{Capabilities: caps(true, true, true)}
	gate := newAutoChainCapabilityGate(startup)
	gate.attach(&stubRuntimeStatusSource{status: &hzruntime.Status{Capabilities: caps(false, false, false)}})

	if gate.enabled(func(c hzruntime.Capabilities) bool { return c.ASRSubmit }) {
		t.Fatal("server 当前 ASRSubmit=false 应拦截(启动快照 true 不应胜出)")
	}
}

// TestAutoChainGateServerNilStatusFallsBack M14:attach 了但 server 状态尚未就绪
// (CurrentRuntimeStatus 返回 nil)时回退启动快照;全为 nil 保守视为不可用。
func TestAutoChainGateServerNilStatusFallsBack(t *testing.T) {
	startup := &hzruntime.Status{Capabilities: caps(true, true, true)}
	gate := newAutoChainCapabilityGate(startup)
	gate.attach(&stubRuntimeStatusSource{status: nil})

	if !gate.enabled(func(c hzruntime.Capabilities) bool { return c.WebDAVUpload }) {
		t.Fatal("server 状态 nil 应回退启动快照(WebDAVUpload=true)")
	}

	nilGate := newAutoChainCapabilityGate(nil)
	nilGate.attach(&stubRuntimeStatusSource{status: nil})
	if nilGate.enabled(func(c hzruntime.Capabilities) bool { return true }) {
		t.Fatal("全为 nil 应保守视为不可用")
	}
}
