import * as React from 'react';
import { AbsoluteFill, interpolate, spring, useCurrentFrame, useVideoConfig } from 'remotion';
import { T } from '../tokens';

// 品牌亮相：Hikami-Go logo + slogan
export const RevealScene: React.FC = () => {
  const f = useCurrentFrame();
  const { fps } = useVideoConfig();
  const titleScale = spring({ frame: f - 10, fps, config: { damping: 12, stiffness: 90 } });
  const titleOpacity = interpolate(f, [8, 22], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' });
  const tagOpacity = interpolate(f, [60, 80], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' });
  return (
    <AbsoluteFill style={{ background: `linear-gradient(135deg, ${T.sceneBg}, ${T.sceneBgAlt})`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexDirection: 'column' }}>
      <div style={{ position: 'absolute', width: 900, height: 900, borderRadius: '50%', background: `radial-gradient(circle, ${T.accentGlow}, transparent 70%)`, opacity: interpolate(f, [0, 60], [0, 0.7]) }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 28, transform: `scale(${titleScale})`, opacity: titleOpacity }}>
        <div style={{
          width: 96, height: 96, borderRadius: 22,
          background: `linear-gradient(135deg, ${T.accent}, ${T.accentDark})`,
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: 64, fontWeight: 800, color: '#fff', fontFamily: T.fontDisplay,
          boxShadow: `0 16px 40px ${T.accentGlow}`,
        }}>H</div>
        <div style={{ fontFamily: T.fontDisplay, fontSize: 132, fontWeight: 700, color: T.heroText, letterSpacing: -2 }}>
          Hikami<span style={{ color: T.accent }}>-Go</span>
        </div>
      </div>
      <div style={{ marginTop: 36, opacity: tagOpacity, display: 'flex', gap: 16 }}>
        {['开源', '免费', '单文件部署'].map((t, i) => (
          <span key={i} style={{ padding: '10px 22px', border: `1px solid ${T.cardBorder}`, borderRadius: 999, color: T.heroCardText, fontSize: 26, background: T.heroCardBg }}>{t}</span>
        ))}
      </div>
    </AbsoluteFill>
  );
};
