// 字幕与场景时间轴。FPS=30。
import type { SceneKind } from './scenes/types';

export interface SceneSpec {
  kind: SceneKind;
  start: number;
  duration: number;
  subtitles: { at: number; text: string }[];
}

export const FPS = 30;
export const THEME = (typeof process !== 'undefined' && process.env.HIKAMI_THEME) || 'midnight';
export const TITLE_SUFFIX = process.env.HIKAMI_TITLE || '';

export const SCENES: SceneSpec[] = [
  {
    kind: 'pain',
    start: 0,
    duration: 15 * FPS,
    subtitles: [
      { at: 0, text: '' },
      { at: 15, text: '关注的直播 越来越多' },
      { at: 75, text: '一场两小时' },
      { at: 105, text: '看不过来' },
      { at: 165, text: '错过 只剩碎片' },
      { at: 225, text: '拼不出完整剧情' },
      { at: 330, text: '' },
    ],
  },
  {
    kind: 'turn',
    start: 15 * FPS,
    duration: 10 * FPS,
    subtitles: [
      { at: 0, text: '' },
      { at: 20, text: '如果——' },
      { at: 55, text: '两小时直播' },
      { at: 90, text: '变成五分钟读完的文字' },
      { at: 165, text: '还自动发到 B 站' },
      { at: 230, text: '' },
    ],
  },
  {
    kind: 'reveal',
    start: 25 * FPS,
    duration: 10 * FPS,
    subtitles: [
      { at: 0, text: '' },
      { at: 35, text: 'Hikami-Go' },
      { at: 110, text: '开源 · 免费 · 单文件部署' },
      { at: 220, text: '' },
    ],
  },
  {
    kind: 'dashboard',
    start: 35 * FPS,
    duration: 28 * FPS,
    subtitles: [
      { at: 0, text: '主播开播 自动录制' },
      { at: 70, text: '弹幕也一起存' },
      { at: 130, text: '录完 自动转写' },
      { at: 200, text: 'AI 生成结构化回顾' },
      { at: 280, text: '最后 自动发布' },
      { at: 360, text: '全程不用点一下' },
      { at: 480, text: '' },
    ],
  },
  {
    kind: 'recap',
    start: 63 * FPS,
    duration: 22 * FPS,
    subtitles: [
      { at: 0, text: '一篇完整的直播回顾' },
      { at: 65, text: '分段 · 小标题 · 重点' },
      { at: 150, text: '五分钟 读完两小时' },
      { at: 240, text: '像一份认真的直播笔记' },
      { at: 420, text: '' },
    ],
  },
  {
    kind: 'replay',
    start: 85 * FPS,
    duration: 18 * FPS,
    subtitles: [
      { at: 0, text: '不只是直播录像' },
      { at: 55, text: '丢一个视频链接' },
      { at: 110, text: '或一个收藏夹' },
      { at: 175, text: '它也能转成回顾' },
      { at: 260, text: '切片素材 · 学习笔记' },
      { at: 380, text: '' },
    ],
  },
  {
    kind: 'closing',
    start: 103 * FPS,
    duration: 22 * FPS,
    subtitles: [
      { at: 0, text: '' },
      { at: 35, text: 'Hikami-Go' },
      { at: 95, text: 'Windows 双击即用' },
      { at: 160, text: 'Linux 一条命令' },
      { at: 220, text: '把追的直播' },
      { at: 265, text: '变成可以慢慢读的文字' },
      { at: 360, text: 'GitHub 搜 Hikami-Go' },
      { at: 440, text: '欢迎 Star ⭐' },
      { at: 560, text: '' },
    ],
  },
];

export const TOTAL_FRAMES = SCENES[SCENES.length - 1].start + SCENES[SCENES.length - 1].duration;
