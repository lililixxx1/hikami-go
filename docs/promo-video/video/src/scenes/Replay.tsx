import * as React from 'react';
import { AbsoluteFill, interpolate, useCurrentFrame } from 'remotion';
import { T } from '../tokens';

// 回放也能用：演示粘贴 URL → 发现回放列表
export const ReplayScene: React.FC = () => {
  const f = useCurrentFrame();
  const enter = interpolate(f, [0, 18], [0, 1], { extrapolateRight: 'clamp' });
  const url = 'https://www.bilibili.com/video/BV1oa3e6venp';
  const typedLen = Math.min(url.length, Math.floor(Math.max(0, f - 30) / 1.5));
  const typed = url.slice(0, typedLen);
  const canDiscover = typedLen >= url.length;
  const discovered = f > 110;
  const rows = [
    { t: 'BV1oa3e6venp · 第 1 P', d: '12:34' },
    { t: 'BV1oa3e6venp · 第 2 P', d: '08:21' },
    { t: 'BV1oa3e6venp · 第 3 P', d: '15:09' },
    { t: 'BV1oa3e6venp · 第 4 P', d: '22:47' },
  ];

  return (
    <AbsoluteFill style={{ background: T.sceneBg, display: 'flex', alignItems: 'center', justifyContent: 'center', opacity: enter }}>
      <div style={{ width: 1200 }}>
        <div style={{ textAlign: 'center', color: T.cardTextMute, fontSize: 18, letterSpacing: 4, marginBottom: 32 }}>发现回放</div>
        <div style={{
          display: 'flex', gap: 12, padding: 14, background: T.cardBg,
          borderRadius: 16, border: `1px solid ${T.cardBorder}`,
        }}>
          <div style={{
            flex: 1, background: T.name === 'light' ? 'rgba(0,0,0,0.04)' : 'rgba(0,0,0,0.3)', borderRadius: 10, padding: '18px 22px',
            fontFamily: T.fontMono, fontSize: 20, color: T.cardText, display: 'flex', alignItems: 'center',
          }}>
            {typed}
            <span style={{ width: 2, height: 22, background: T.accent, marginLeft: 2, opacity: Math.floor(f / 15) % 2 ? 1 : 0 }} />
          </div>
          <div style={{
            padding: '0 32px', borderRadius: 10,
            background: canDiscover ? T.accent : (T.name === 'light' ? 'rgba(0,0,0,0.06)' : 'rgba(255,255,255,0.08)'),
            color: canDiscover ? '#fff' : T.cardTextMute,
            display: 'flex', alignItems: 'center', fontWeight: 700, fontSize: 18,
            boxShadow: canDiscover ? `0 8px 24px ${T.accentGlow}` : 'none',
          }}>发现</div>
        </div>

        <div style={{ marginTop: 28, opacity: discovered ? 1 : 0, transition: 'opacity 0.4s' }}>
          <div style={{ fontSize: 14, color: T.cardTextMute, marginBottom: 12 }}>发现 4 条回放</div>
          <div style={{ background: T.cardBg, borderRadius: 12, overflow: 'hidden', border: `1px solid ${T.cardBorder}` }}>
            {rows.map((r, i) => {
              const reveal = f > 120 + i * 10;
              return (
                <div key={i} style={{
                  display: 'flex', alignItems: 'center', gap: 18, padding: '16px 22px',
                  borderBottom: i < rows.length - 1 ? `1px solid ${T.cardBorder}` : 'none',
                  opacity: reveal ? 1 : 0, transform: `translateX(${reveal ? 0 : -20}px)`,
                }}>
                  <div style={{ width: 56, height: 36, borderRadius: 6, background: 'linear-gradient(135deg,#e85d04,#d00000)', flexShrink: 0 }} />
                  <div style={{ flex: 1 }}>
                    <div style={{ color: T.cardText, fontSize: 16, fontWeight: 600 }}>{r.t}</div>
                    <div style={{ color: T.cardTextMute, fontSize: 13, fontFamily: T.fontMono }}>{r.d}</div>
                  </div>
                  <div style={{ padding: '6px 14px', borderRadius: 8, background: T.accent, color: '#fff', fontSize: 13, fontWeight: 600 }}>转回顾</div>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </AbsoluteFill>
  );
};
