// 设计 token · 多主题。通过 process.env.HIKAMI_THEME 选择。
// 4 个主题：midnight(默认深蓝) / light(明亮软件UI) / amber(暖色) / mono(极简黑白)
//
// 语义色约定（场景组件统一用这些，不要直接用 bgDark/ink 等）：
//   sceneBg     —— 场景背景（铺满全屏的底色）
//   sceneBgAlt  —— 场景背景的次级色（渐变/卡片层）
//   cardBg      —— 场景内卡片的底色
//   cardText    —— 卡片上的主文字色
//   cardTextMute—— 卡片上的次文字色
//   cardBorder  —— 卡片边框
//   heroText    —— 大标题/Logo 文字色（reveal/closing 场景的中央大字）
//   heroMuted   —— 大标题下方的副文字色
//   subBg/subBorder/subText —— 字幕条

export type ThemeName = 'midnight' | 'light' | 'amber' | 'mono';

export interface Theme {
  name: ThemeName;
  label: string;
  accent: string;
  accentDark: string;
  accentGlow: string;
  surface: string;
  surfaceAlt: string;
  ink: string;
  inkMuted: string;
  inkSoft: string;
  line: string;
  live: string;
  recording: string;
  success: string;
  warn: string;
  bgDark: string;
  bgDarkAlt: string;
  // 语义色（场景组件统一读这些）
  sceneBg: string;
  sceneBgAlt: string;
  cardBg: string;
  cardText: string;
  cardTextMute: string;
  cardBorder: string;
  heroText: string;
  heroMuted: string;
  heroCardBg: string;
  heroCardText: string;
  subBg: string;
  subBorder: string;
  subText: string;
  fontDisplay: string;
  fontText: string;
  fontMono: string;
}

const BASE_FONT = {
  fontDisplay: '"Sora","PingFang SC","Microsoft YaHei",sans-serif',
  fontText: '"PingFang SC","Microsoft YaHei","Helvetica Neue",sans-serif',
  fontMono: '"JetBrains Mono","Consolas",monospace',
};

export const THEMES: Record<ThemeName, Theme> = {
  midnight: {
    name: 'midnight',
    label: '深蓝（默认）',
    accent: '#0066cc', accentDark: '#0052a3', accentGlow: 'rgba(0,102,204,0.35)',
    surface: '#fafaf9', surfaceAlt: '#f0eee9',
    ink: '#1a1917', inkMuted: '#9e9890', inkSoft: '#5a544c',
    line: 'rgba(0,0,0,0.07)',
    live: '#e85d04', recording: '#d00000', success: '#1a7a5c', warn: '#b8860b',
    bgDark: '#0a0f1a', bgDarkAlt: '#10172a',
    sceneBg: '#0a0f1a', sceneBgAlt: '#10172a',
    cardBg: 'rgba(28,36,56,0.9)', cardText: '#e8e6e1', cardTextMute: '#8a92a6', cardBorder: 'rgba(255,255,255,0.08)',
    heroText: '#ffffff', heroMuted: '#9e9890',
    heroCardBg: 'rgba(255,255,255,0.06)', heroCardText: '#cfcdd6',
    subBg: 'rgba(8,12,22,0.66)', subBorder: 'rgba(255,255,255,0.08)', subText: '#ffffff',
    ...BASE_FONT,
  },
  light: {
    name: 'light',
    label: '明亮（贴近软件 UI）',
    accent: '#0066cc', accentDark: '#0052a3', accentGlow: 'rgba(0,102,204,0.18)',
    surface: '#fafaf9', surfaceAlt: '#f0eee9',
    ink: '#1a1917', inkMuted: '#9e9890', inkSoft: '#5a544c',
    line: 'rgba(0,0,0,0.08)',
    live: '#e85d04', recording: '#d00000', success: '#1a7a5c', warn: '#b8860b',
    bgDark: '#eef1f6', bgDarkAlt: '#e2e7ef',
    sceneBg: '#fafaf9', sceneBgAlt: '#f0eee9',
    cardBg: '#ffffff', cardText: '#1a1917', cardTextMute: '#9e9890', cardBorder: 'rgba(0,0,0,0.08)',
    heroText: '#1a1917', heroMuted: '#5a544c',
    heroCardBg: 'rgba(255,255,255,0.92)', heroCardText: '#1a1917',
    subBg: 'rgba(255,255,255,0.92)', subBorder: 'rgba(0,0,0,0.08)', subText: '#1a1917',
    ...BASE_FONT,
  },
  amber: {
    name: 'amber',
    label: '暖色（琥珀）',
    accent: '#d97706', accentDark: '#b45309', accentGlow: 'rgba(217,119,6,0.32)',
    surface: '#fdf8f0', surfaceAlt: '#f5ece0',
    ink: '#2a1f15', inkMuted: '#9c8466', inkSoft: '#5a4631',
    line: 'rgba(60,40,10,0.1)',
    live: '#dc2626', recording: '#b91c1c', success: '#15803d', warn: '#a16207',
    bgDark: '#1c1410', bgDarkAlt: '#2a1f17',
    sceneBg: '#1c1410', sceneBgAlt: '#2a1f17',
    cardBg: 'rgba(58,42,28,0.92)', cardText: '#fdf8f0', cardTextMute: '#b8a386', cardBorder: 'rgba(217,119,6,0.2)',
    heroText: '#fdf8f0', heroMuted: '#9c8466',
    heroCardBg: 'rgba(253,248,240,0.06)', heroCardText: '#f0e0c8',
    subBg: 'rgba(28,20,16,0.7)', subBorder: 'rgba(217,119,6,0.2)', subText: '#fdf8f0',
    ...BASE_FONT,
  },
  mono: {
    name: 'mono',
    label: '极简（黑白）',
    accent: '#1a1917', accentDark: '#000000', accentGlow: 'rgba(0,0,0,0.25)',
    surface: '#ffffff', surfaceAlt: '#f2f2f0',
    ink: '#0a0a0a', inkMuted: '#8a8a8a', inkSoft: '#4a4a4a',
    line: 'rgba(0,0,0,0.12)',
    live: '#d00000', recording: '#000000', success: '#1a1917', warn: '#8a8a8a',
    bgDark: '#0a0a0a', bgDarkAlt: '#1a1a1a',
    sceneBg: '#0a0a0a', sceneBgAlt: '#1a1a1a',
    cardBg: 'rgba(26,26,26,0.92)', cardText: '#f5f5f5', cardTextMute: '#8a8a8a', cardBorder: 'rgba(255,255,255,0.18)',
    heroText: '#ffffff', heroMuted: '#8a8a8a',
    heroCardBg: 'rgba(255,255,255,0.06)', heroCardText: '#e0e0e0',
    subBg: 'rgba(10,10,10,0.78)', subBorder: 'rgba(255,255,255,0.18)', subText: '#ffffff',
    ...BASE_FONT,
  },
};

// 默认主题（模块级，供组件 import { T } 用）。
// 由 setActiveTheme() 在 Main 顶层根据 inputProps 切换。
// 这样不依赖 process.env（浏览器端无 process.env）。
let ACTIVE: ThemeName = 'midnight';

export const setActiveTheme = (name: ThemeName) => {
  if (THEMES[name]) ACTIVE = name;
};

export const getTheme = (): Theme => THEMES[ACTIVE];

// 兼容旧代码：导出当前主题的 token。
// 注意：组件渲染时取值即可（Remotion 每帧重新渲染整个组件树）。
export const T = new Proxy({} as Theme, {
  get(_, key: string) {
    return (THEMES[ACTIVE] as Record<string, unknown>)[key];
  },
});
