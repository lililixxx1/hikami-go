import { Composition } from 'remotion';
import { Main } from './Main';
import { TOTAL_FRAMES, FPS } from './script';
import type { ThemeName } from './tokens';

export interface RootProps {
  theme?: ThemeName;
  titleSuffix?: string;
}

export const RemotionRoot: React.FC = () => {
  return (
    <Composition
      id="Main"
      component={Main}
      durationInFrames={TOTAL_FRAMES}
      fps={FPS}
      width={1920}
      height={1080}
      defaultProps={{ theme: 'midnight' as ThemeName, titleSuffix: '' }}
    />
  );
};
