import * as React from 'react';
import { AbsoluteFill, interpolate, spring, useCurrentFrame, useVideoConfig } from 'remotion';
import { T } from '../tokens';

// 收尾：logo + GitHub CTA + Star
export const ClosingScene: React.FC = () => {
  const f = useCurrentFrame();
  const { fps } = useVideoConfig();
  const logoScale = spring({ frame: f - 5, fps, config: { damping: 12 } });
  const ctaOpacity = interpolate(f, [60, 90], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' });
  const starOpacity = interpolate(f, [120, 160], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' });
  const starFloat = Math.sin(f * 0.08) * 8;

  return (
    <AbsoluteFill style={{ background: `linear-gradient(135deg, ${T.sceneBg}, ${T.sceneBgAlt})`, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
      <div style={{ position: 'absolute', width: 1100, height: 1100, borderRadius: '50%', background: `radial-gradient(circle, ${T.accentGlow}, transparent 65%)`, opacity: interpolate(f, [0, 80], [0, 0.6]) }} />

      <div style={{ transform: `scale(${logoScale})`, display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 24, marginBottom: 8 }}>
          <div style={{
            width: 80, height: 80, borderRadius: 20,
            background: `linear-gradient(135deg, ${T.accent}, ${T.accentDark})`,
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            fontSize: 52, fontWeight: 800, color: '#fff', fontFamily: T.fontDisplay,
            boxShadow: `0 12px 32px ${T.accentGlow}`,
          }}>H</div>
          <div style={{ fontFamily: T.fontDisplay, fontSize: 108, fontWeight: 700, color: T.heroText, letterSpacing: -2 }}>
            Hikami<span style={{ color: T.accent }}>-Go</span>
          </div>
        </div>
        <div style={{ color: T.heroMuted, fontSize: 22, letterSpacing: 4, marginTop: 4 }}>Go 后端 · 内嵌 Web 界面 · 单文件部署</div>
      </div>

      <div style={{ marginTop: 60, opacity: ctaOpacity, display: 'flex', gap: 18 }}>
        {[
          { label: 'Windows 双击即用', icon: '⤓' },
          { label: 'Linux 一条命令', icon: '⌘' },
          { label: 'GitHub 搜 Hikami-Go', icon: '⌥' },
        ].map((t, i) => (
          <div key={i} style={{
            padding: '16px 26px', borderRadius: 14,
            background: T.heroCardBg, border: `1px solid ${T.cardBorder}`,
            color: T.heroCardText, fontSize: 20, fontWeight: 600, display: 'flex', alignItems: 'center', gap: 12,
          }}>
            <span style={{ fontSize: 22 }}>{t.icon}</span>{t.label}
          </div>
        ))}
      </div>

      <div style={{ marginTop: 50, opacity: starOpacity, transform: `translateY(${starFloat}px)`, display: 'flex', alignItems: 'center', gap: 14 }}>
        <span style={{ fontSize: 56, color: '#ffd54a', filter: 'drop-shadow(0 4px 16px rgba(255,213,74,0.6))' }}>★</span>
        <span style={{ color: '#ffd54a', fontSize: 36, fontWeight: 700, fontFamily: T.fontDisplay }}>欢迎 Star</span>
      </div>

      <div style={{ position: 'absolute', bottom: 40, color: T.heroMuted, fontSize: 14, letterSpacing: 2 }}>
        把追的直播，变成可以慢慢读的文字
      </div>
    </AbsoluteFill>
  );
};
