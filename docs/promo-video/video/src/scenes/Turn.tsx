import * as React from 'react';
import { AbsoluteFill, Img, interpolate, useCurrentFrame, staticFile } from 'remotion';
import { T } from '../tokens';

// 转折：hero 图淡入，遮罩聚焦中央
export const TurnScene: React.FC = () => {
  const f = useCurrentFrame();
  const opacity = interpolate(f, [0, 18], [0, 1], { extrapolateRight: 'clamp' });
  const zoom = interpolate(f, [0, 300], [1.08, 1.18]);
  // 明亮主题下，hero 图本身是深色调，叠一层半透明白使其融入浅色背景
  const veil = T.name === 'light' ? 'rgba(250,250,249,0.45)' : 'transparent';
  return (
    <AbsoluteFill style={{ background: T.sceneBg }}>
      <AbsoluteFill style={{ opacity }}>
        <Img src={staticFile('/images/hero.png')} style={{ width: '100%', height: '100%', objectFit: 'cover', transform: `scale(${zoom})` }} />
      </AbsoluteFill>
      <AbsoluteFill style={{ background: veil }} />
      <AbsoluteFill style={{ background: T.name === 'light' ? 'radial-gradient(circle at 50% 55%, transparent 30%, rgba(250,250,249,0.7) 90%)' : 'radial-gradient(circle at 50% 55%, transparent 30%, rgba(8,12,22,0.7) 90%)' }} />
    </AbsoluteFill>
  );
};
