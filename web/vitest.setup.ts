// Node ≥25 暴露实验性原生 localStorage 全局:未启用 --localstorage-file 时它是一个
// 没有 getItem/setItem 方法的对象,且早于 happy-dom 装配占据 globalThis,导致模块
// 顶层访问 localStorage 的代码(如 useAdminToken.ts 的令牌迁移)在测试收集阶段即抛
// "localStorage.getItem is not a function"。Node 20(CI)无此全局,守卫不触发。
// 此处以等价内存实现替换;setupFiles 按测试文件各跑一次,每个文件获得独立存储,
// 与 happy-dom 每文件独立 window 的隔离语义一致。
const g = globalThis as { localStorage?: Storage } & typeof globalThis

if (typeof g.localStorage?.getItem !== 'function') {
  const store = new Map<string, string>()
  g.localStorage = {
    getItem: (key: string) => (store.has(key) ? (store.get(key) as string) : null),
    setItem: (key: string, value: string) => {
      store.set(key, String(value))
    },
    removeItem: (key: string) => {
      store.delete(key)
    },
    clear: () => {
      store.clear()
    },
    key: (index: number) => Array.from(store.keys())[index] ?? null,
    get length() {
      return store.size
    },
  }
}
