import './index.css';
import * as React from 'react';
import { AbsoluteFill, Sequence, useCurrentFrame } from 'remotion';
import { SCENES } from './script';
import { PainScene } from './scenes/Pain';
import { TurnScene } from './scenes/Turn';
import { RevealScene } from './scenes/Reveal';
import { DashboardScene } from './scenes/Dashboard';
import { RecapScene } from './scenes/Recap';
import { ReplayScene } from './scenes/Replay';
import { ClosingScene } from './scenes/Closing';
import { Subtitle } from './components/Subtitle';
import { T, setActiveTheme, type ThemeName } from './tokens';

export interface MainProps {
  theme?: ThemeName;
  titleSuffix?: string;
}

export const Main: React.FC<MainProps> = ({ theme, titleSuffix }) => {
  // 每次渲染根据 props 切主题（inputProps 从 CLI --props 传入）
  React.useMemo(() => {
    setActiveTheme((theme as ThemeName) || 'midnight');
  }, [theme]);
  const TITLE_SUFFIX = titleSuffix || '';

  return (
    <AbsoluteFill style={{ backgroundColor: T.sceneBg, fontFamily: T.fontText, color: T.subText, overflow: 'hidden' }}>
      {SCENES.map((s) => (
        <Sequence key={s.kind} from={s.start} durationInFrames={s.duration} name={s.kind}>
          <SceneSwitch kind={s.kind} />
          <SubtitleOverlay subtitles={s.subtitles} sceneDuration={s.duration} />
        </Sequence>
      ))}
      {/* 全局轻微暗角（仅深色主题） */}
      {T.name !== 'light' && (
        <div style={{ position: 'absolute', inset: 0, pointerEvents: 'none', boxShadow: 'inset 0 0 240px rgba(0,0,0,0.55)' }} />
      )}
      {/* 标题角标（多版本区分用） */}
      {TITLE_SUFFIX ? (
        <div style={{ position: 'absolute', top: 28, right: 36, color: T.heroMuted, opacity: 0.5, fontSize: 22, fontFamily: T.fontDisplay, fontWeight: 600, letterSpacing: 1 }}>
          {TITLE_SUFFIX}
        </div>
      ) : null}
    </AbsoluteFill>
  );
};

const SceneSwitch: React.FC<{ kind: string }> = ({ kind }) => {
  switch (kind) {
    case 'pain': return <PainScene />;
    case 'turn': return <TurnScene />;
    case 'reveal': return <RevealScene />;
    case 'dashboard': return <DashboardScene />;
    case 'recap': return <RecapScene />;
    case 'replay': return <ReplayScene />;
    case 'closing': return <ClosingScene />;
    default: return null;
  }
};

const SubtitleOverlay: React.FC<{ subtitles: { at: number; text: string }[]; sceneDuration: number }> = ({ subtitles }) => {
  const frame = useCurrentFrame();
  let current = '';
  for (let i = 0; i < subtitles.length; i++) {
    if (frame >= subtitles[i].at) {
      if (subtitles[i].text) current = subtitles[i].text;
      else current = '';
    }
  }
  return <Subtitle text={current} />;
};
