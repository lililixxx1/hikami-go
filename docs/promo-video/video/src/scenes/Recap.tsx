import * as React from 'react';
import { AbsoluteFill, Img, interpolate, useCurrentFrame, staticFile } from 'remotion';
import { T } from '../tokens';

// 回顾生成段：背景 AI 图 + 前景重绘的回顾 markdown 文档
export const RecapScene: React.FC = () => {
  const f = useCurrentFrame();
  const bgOpacity = interpolate(f, [0, 18], [0, 1], { extrapolateRight: 'clamp' });
  const docY = interpolate(f, [20, 60], [80, 0], { extrapolateRight: 'clamp' });
  const docOpacity = interpolate(f, [20, 50], [0, 1], { extrapolateRight: 'clamp' });
  const zoom = interpolate(f, [0, 600], [1.05, 1.12]);
  // 明亮主题：弱化背景图（变浅），让前景文档融入浅色风
  const bgVeil = T.name === 'light' ? 'brightness(1.4) saturate(0.5)' : 'brightness(0.55) blur(2px)';
  const bgGradient = T.name === 'light'
    ? 'linear-gradient(105deg, rgba(250,250,249,0.92) 40%, transparent 80%)'
    : 'linear-gradient(105deg, rgba(8,12,22,0.85) 40%, transparent 80%)';

  return (
    <AbsoluteFill style={{ background: T.sceneBg }}>
      <AbsoluteFill style={{ opacity: bgOpacity }}>
        <Img src={staticFile('/images/recap-doc.png')} style={{ width: '100%', height: '100%', objectFit: 'cover', transform: `scale(${zoom})`, filter: bgVeil }} />
      </AbsoluteFill>
      <AbsoluteFill style={{ background: bgGradient }} />
      {/* 前景：重绘的回顾文档 */}
      <div style={{
        position: 'absolute', left: 110, top: 120, width: 820,
        transform: `translateY(${docY}px)`, opacity: docOpacity,
        background: T.surface, borderRadius: 14, padding: '36px 44px',
        boxShadow: T.name === 'light' ? '0 16px 40px rgba(0,0,0,0.12)' : '0 30px 80px rgba(0,0,0,0.6)',
        fontFamily: T.fontText, color: T.ink, border: T.name === 'light' ? `1px solid ${T.line}` : 'none',
      }}>
        <div style={{ fontSize: 14, color: T.inkMuted, marginBottom: 6 }}>直播回顾</div>
        <div style={{ fontFamily: T.fontDisplay, fontSize: 30, fontWeight: 700, marginBottom: 18, lineHeight: 1.2, color: T.ink }}>
          悠闲夜晚与种田 · 灰泽满 Hazel
        </div>
        <div style={{ fontSize: 13, color: T.inkMuted, marginBottom: 20, borderBottom: `1px solid ${T.line}`, paddingBottom: 14 }}>
          2026-08-06 · 20:35 → 23:39 · 约 3 小时
        </div>
        <Block title="开场 · 杂谈" lines={['今天的状态比较松弛，先和弹幕聊了一会儿近况', '提到了最近在玩的几款游戏，准备种田向的内容']} highlight={f > 120} />
        <Block title="主要内容 · 游戏环节" lines={['开始进入种田游戏，先是规划了农场布局', '中途遇到几次小意外，弹幕互动很活跃', '聊到游戏设计背后的逻辑']} highlight={f > 240} />
        <Block title="收尾 · 杂谈" lines={['逐渐进入深夜模式，话题偏生活向', '感谢观众陪伴，预告下一场']} highlight={f > 360} />
      </div>
      {/* 右侧装饰：要点高亮 */}
      <div style={{
        position: 'absolute', right: 100, top: '50%', transform: 'translateY(-50%)',
        opacity: interpolate(f, [180, 230], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' }),
        display: 'flex', flexDirection: 'column', gap: 20, alignItems: 'flex-start',
      }}>
        {['分段结构', '小标题', '重点高亮', '5 分钟读完'].map((t, i) => (
          <div key={i} style={{
            padding: '14px 26px', borderRadius: 999,
            background: T.heroCardBg, backdropFilter: 'blur(8px)',
            border: `1px solid ${T.cardBorder}`, color: T.heroCardText,
            fontSize: 22, fontWeight: 600, fontFamily: T.fontDisplay,
          }}>{t}</div>
        ))}
      </div>
    </AbsoluteFill>
  );
};

const Block: React.FC<{ title: string; lines: string[]; highlight: boolean }> = ({ title, lines, highlight }) => (
  <div style={{ marginBottom: 22, transition: 'opacity 0.4s', opacity: highlight ? 1 : 0.5 }}>
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 8 }}>
      <span style={{ width: 4, height: 18, background: highlight ? T.accent : T.inkMuted, borderRadius: 2 }} />
      <span style={{ fontSize: 18, fontWeight: 700, color: T.ink }}>{title}</span>
    </div>
    {lines.map((l, i) => (
      <div key={i} style={{ fontSize: 14, color: T.inkSoft, lineHeight: 1.7, paddingLeft: 14, borderLeft: highlight ? `2px solid ${T.accentGlow}` : '2px solid transparent' }}>{l}</div>
    ))}
  </div>
);
