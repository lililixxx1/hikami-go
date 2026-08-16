import * as React from 'react';
import { AbsoluteFill, interpolate, useCurrentFrame } from 'remotion';
import { T } from '../tokens';

// 重绘 Hikami-Go 主播管理 + 回顾列表界面（基于真实 UI token）
// 4 段流程动画：录制 → 转写 → 回顾 → 发布，通过卡片高亮依次推进

const ROWS = [
  { t: '悠闲夜晚与种田', m: '2026/08/06', s: '已发布', dur: '3h04m' },
  { t: '电话一会儿', m: '2026/08/05', s: '已发布', dur: '59m' },
  { t: '午间电台', m: '2026/08/05', s: '已发布', dur: '47m' },
  { t: '悠闲夜晚', m: '2026/08/04', s: '已发布', dur: '3h00m' },
  { t: '悠闲夜晚 偶像经纪人', m: '2026/08/02', s: '已发布', dur: '2h21m' },
];

export const DashboardScene: React.FC = () => {
  const f = useCurrentFrame();
  // 整体淡入
  const enter = interpolate(f, [0, 18], [0, 1], { extrapolateRight: 'clamp' });
  // 流程高亮阶段（每段 ~150 帧 = 5s，4 段 = 600 帧 = 20s）
  const phase = Math.min(3, Math.floor((f - 60) / 150));
  const phaseActive = (i: number) => f >= 60 && phase === i ? 1 : 0;

  // 鼠标位置（在某行上方）
  const mouseRow = Math.min(ROWS.length - 1, Math.floor((f / 12) % ROWS.length));

  return (
    <AbsoluteFill style={{ background: T.sceneBg, opacity: enter }}>
      {/* 顶部导航 */}
      <div style={{ height: 64, background: '#fff', borderBottom: `1px solid ${T.line}`, display: 'flex', alignItems: 'center', padding: '0 28px', gap: 24 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <div style={{ width: 32, height: 32, borderRadius: 8, background: `linear-gradient(135deg, ${T.accent}, ${T.accentDark})`, color: '#fff', display: 'flex', alignItems: 'center', justifyContent: 'center', fontWeight: 800, fontSize: 18 }}>H</div>
          <span style={{ fontFamily: T.fontDisplay, fontWeight: 700, fontSize: 20, color: T.ink }}>Hikami-Go</span>
        </div>
        {['首页', '主播管理', '回顾管理', '设置'].map((t, i) => (
          <div key={t} style={{ padding: '8px 16px', borderRadius: 8, fontSize: 15, fontWeight: i === 2 ? 700 : 500, color: i === 2 ? T.accent : T.inkSoft, background: i === 2 ? 'rgba(0,102,204,0.08)' : 'transparent' }}>{t}</div>
        ))}
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 6 }}>
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: T.success }} />
          <span style={{ fontSize: 13, color: T.inkMuted }}>已连接</span>
        </div>
      </div>

      {/* 主区域：左侧 4 步流程图 + 右侧场次列表 */}
      <div style={{ display: 'flex', height: 'calc(100% - 64px)' }}>
        {/* 左：流程面板 */}
        <div style={{ width: 540, padding: 32, background: T.surfaceAlt, borderRight: `1px solid ${T.line}`, display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 22 }}>
          <div style={{ fontSize: 14, color: T.inkMuted, letterSpacing: 2, marginBottom: -6 }}>自动任务流水线</div>
          {[
            { icon: '⏺', label: '录播', desc: '自动录制音视频 + 弹幕' },
            { icon: '♪', label: '转写', desc: '语音识别 · 时间轴文字稿' },
            { icon: '✦', label: 'AI 回顾', desc: '大模型生成结构化回顾' },
            { icon: '↗', label: '发布', desc: '自动发到 B 站动态' },
          ].map((step, i) => {
            const active = phaseActive(i);
            return (
              <React.Fragment key={i}>
                <div style={{
                  display: 'flex', alignItems: 'center', gap: 18,
                  padding: '18px 22px', borderRadius: 14,
                  background: active ? '#fff' : 'rgba(255,255,255,0.5)',
                  border: `1px solid ${active ? T.accent : 'transparent'}`,
                  boxShadow: active ? `0 12px 30px ${T.accentGlow}` : '0 2px 8px rgba(0,0,0,0.04)',
                  transform: `scale(${1 + active * 0.04})`,
                  transition: 'all 0.3s',
                }}>
                  <div style={{ width: 44, height: 44, borderRadius: 12, background: active ? T.accent : 'rgba(0,102,204,0.12)', color: active ? '#fff' : T.accent, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 22, fontWeight: 700 }}>{step.icon}</div>
                  <div>
                    <div style={{ fontSize: 19, fontWeight: 700, color: T.ink, fontFamily: T.fontDisplay }}>{step.label}</div>
                    <div style={{ fontSize: 13, color: T.inkMuted, marginTop: 2 }}>{step.desc}</div>
                  </div>
                  {active ? <div style={{ marginLeft: 'auto', width: 18, height: 18, borderRadius: '50%', border: `3px solid ${T.accent}`, borderTopColor: 'transparent', animation: 'spin 0.8s linear infinite' }} /> : null}
                </div>
                {i < 3 ? (
                  <div style={{ marginLeft: 40, height: 18, width: 2, background: phase > i ? T.accent : T.line, opacity: 0.5 }} />
                ) : null}
              </React.Fragment>
            );
          })}
        </div>

        {/* 右：场次列表 */}
        <div style={{ flex: 1, padding: 28, overflow: 'hidden' }}>
          <div style={{ fontSize: 13, color: T.inkMuted, marginBottom: 14, display: 'flex', gap: 10 }}>
            <span style={{ fontWeight: 700, color: T.ink }}>录播</span>
            <span style={{ color: T.inkMuted }}>回放</span>
          </div>
          <div style={{ background: '#fff', borderRadius: 12, border: `1px solid ${T.line}`, overflow: 'hidden' }}>
            <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr', padding: '12px 18px', background: T.surfaceAlt, fontSize: 12, color: T.inkMuted, fontWeight: 600, letterSpacing: 1, borderBottom: `1px solid ${T.line}` }}>
              <div>标题</div><div>主播</div><div>状态</div><div>时长</div>
            </div>
            {ROWS.map((r, i) => {
              const hover = mouseRow === i;
              return (
                <div key={i} style={{
                  display: 'grid', gridTemplateColumns: '2fr 1fr 1fr 1fr',
                  padding: '16px 18px', alignItems: 'center', fontSize: 14, color: T.ink,
                  background: hover ? 'rgba(0,102,204,0.05)' : '#fff',
                  borderBottom: i < ROWS.length - 1 ? `1px solid ${T.line}` : 'none',
                }}>
                  <div style={{ fontWeight: 600 }}>{r.t}</div>
                  <div style={{ color: T.inkMuted, fontSize: 13 }}>灰泽满Hazel</div>
                  <div>
                    <span style={{ padding: '3px 10px', borderRadius: 6, fontSize: 12, fontWeight: 600, background: 'rgba(26,122,92,0.1)', color: T.success }}>{r.s}</span>
                  </div>
                  <div style={{ color: T.inkMuted, fontSize: 13, fontFamily: T.fontMono }}>{r.dur}</div>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </AbsoluteFill>
  );
};
