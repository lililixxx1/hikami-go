import * as React from 'react';
import { AbsoluteFill, interpolate, useCurrentFrame } from 'remotion';
import { T } from '../tokens';

// 痛点场景：一堆飘动的"直播中"卡片堆叠，红点闪烁
const STREAMS = [
  { t: '悠闲夜晚与种田', x: 140, y: 180, w: 380, h: 220, r: -6, d: 0 },
  { t: '突击午台！调整作息中', x: 600, y: 140, w: 420, h: 240, r: 4, d: 0.3 },
  { t: '通关INSIDE', x: 1120, y: 200, w: 360, h: 210, r: -3, d: 0.6 },
  { t: '简短的萤火虫repo', x: 240, y: 540, w: 400, h: 230, r: 5, d: 0.9 },
  { t: '午间电台', x: 720, y: 580, w: 360, h: 200, r: -4, d: 1.2 },
  { t: '电话一会儿', x: 1180, y: 540, w: 340, h: 220, r: 7, d: 1.5 },
];

export const PainScene: React.FC = () => {
  const f = useCurrentFrame();
  return (
    <AbsoluteFill style={{ background: `radial-gradient(circle at 50% 40%, ${T.sceneBgAlt}, ${T.sceneBg})` }}>
      {STREAMS.map((s, i) => {
        const inOpacity = interpolate(f - i * 6, [0, 14], [0, 1], { extrapolateRight: 'clamp', extrapolateLeft: 'clamp' });
        const drift = Math.sin((f + i * 30) * 0.02) * 6;
        const blink = (Math.floor(f / 15) % 2 === 0) ? 1 : 0.35;
        return (
          <div
            key={i}
            style={{
              position: 'absolute',
              left: s.x, top: s.y + drift,
              width: s.w, height: s.h,
              transform: `rotate(${s.r}deg)`,
              opacity: inOpacity,
              background: T.cardBg,
              borderRadius: 16,
              border: `1px solid ${T.cardBorder}`,
              boxShadow: T.name === 'light' ? '0 12px 32px rgba(0,0,0,0.1)' : '0 24px 60px rgba(0,0,0,0.55)',
              padding: 18,
              display: 'flex', flexDirection: 'column', justifyContent: 'space-between',
              overflow: 'hidden',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span style={{ width: 10, height: 10, borderRadius: '50%', background: T.live, opacity: blink, boxShadow: `0 0 10px ${T.live}` }} />
              <span style={{ color: T.live, fontSize: 18, fontWeight: 700, letterSpacing: 1, opacity: blink }}>直播中</span>
              <span style={{ marginLeft: 'auto', color: T.cardTextMute, fontSize: 14 }}>2h+</span>
            </div>
            <div style={{ color: T.cardText, fontSize: 26, fontWeight: 600, fontFamily: T.fontDisplay }}>{s.t}</div>
            <div style={{ height: 4, background: T.cardBorder, borderRadius: 2, overflow: 'hidden' }}>
              <div style={{ height: '100%', width: '70%', background: T.accent, opacity: 0.6 }} />
            </div>
          </div>
        );
      })}
      <div style={{
        position: 'absolute', left: 0, right: 0, top: 60,
        textAlign: 'center', color: T.cardTextMute, opacity: interpolate(f, [60, 80], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' }),
      }}>
        <div style={{ fontSize: 22, letterSpacing: 6 }}>你关注的</div>
      </div>
    </AbsoluteFill>
  );
};
