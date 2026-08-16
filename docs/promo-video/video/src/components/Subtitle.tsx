import * as React from 'react';
import { interpolate, spring, useCurrentFrame, useVideoConfig } from 'remotion';
import { T } from '../tokens';

export const Subtitle: React.FC<{ text: string }> = ({ text }) => {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  if (!text) return null;
  // 入场：前 8 帧淡入 + 轻微上移；出场不做（被下一条覆盖）
  const opacity = interpolate(frame, [-2, 6], [0, 1], { extrapolateRight: 'clamp', extrapolateLeft: 'clamp' });
  const ty = interpolate(frame, [-2, 8], [12, 0], { extrapolateRight: 'clamp', extrapolateLeft: 'clamp' });
  return (
    <div
      style={{
        position: 'absolute',
        left: 0, right: 0, bottom: 96,
        display: 'flex', justifyContent: 'center',
        opacity, transform: `translateY(${ty}px)`,
        pointerEvents: 'none',
      }}
    >
      <div
        style={{
          padding: '18px 44px',
          borderRadius: 18,
          background: T.subBg,
          backdropFilter: 'blur(10px)',
          border: `1px solid ${T.subBorder}`,
          color: T.subText,
          fontSize: 56,
          fontWeight: 700,
          letterSpacing: 2,
          textAlign: 'center',
          maxWidth: '82%',
          fontFamily: T.fontDisplay,
          textShadow: T.name === 'light' ? '0 2px 12px rgba(0,0,0,0.08)' : '0 2px 24px rgba(0,0,0,0.6)',
        }}
      >
        {text}
      </div>
    </div>
  );
};
