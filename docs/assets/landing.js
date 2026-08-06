/* docs/assets/landing.js
   纯原生 JS,无依赖。
   - IntersectionObserver 驱动 .reveal 入场动画(尊重 prefers-reduced-motion)
   - 检测访客操作系统,在 macOS/Linux 上把"Windows 下载"按钮降级为"查看全部下载"
   - 顶部下载按钮的小气泡提示跟随滚动显隐
*/
(function () {
  'use strict';

  var reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // ===== 入场动画 =====
  function initReveal() {
    var els = document.querySelectorAll('.reveal');
    if (reduceMotion || !('IntersectionObserver' in window)) {
      els.forEach(function (el) { el.classList.add('is-visible'); });
      return;
    }
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (entry.isIntersecting) {
          entry.target.classList.add('is-visible');
          io.unobserve(entry.target);
        }
      });
    }, { threshold: 0.12, rootMargin: '0px 0px -40px 0px' });
    els.forEach(function (el) { io.observe(el); });
  }

  // ===== 平台检测:非 Windows 时柔化主 CTA 文案 =====
  function detectPlatform() {
    var ua = navigator.userAgent || '';
    var isWindows = /Windows/.test(ua);
    if (isWindows) return;

    // macOS / Linux / 其他:主下载按钮文案改为"查看下载",避免误导
    document.querySelectorAll('[data-cta="download"]').forEach(function (el) {
      var txt = el.querySelector('[data-cta-text]');
      if (txt) txt.textContent = '查看全部下载';
    });
  }

  // ===== 顶部 nav 在 Hero 滚出后加阴影 =====
  function initNavShadow() {
    var nav = document.querySelector('.nav');
    if (!nav) return;
    function onScroll() {
      if (window.scrollY > 8) nav.style.boxShadow = 'var(--shadow-sm)';
      else nav.style.boxShadow = 'none';
    }
    window.addEventListener('scroll', onScroll, { passive: true });
    onScroll();
  }

  // ===== 平滑滚动到锚点(nav 链接) =====
  function initSmoothScroll() {
    document.querySelectorAll('a[href^="#"]').forEach(function (a) {
      a.addEventListener('click', function (e) {
        var id = a.getAttribute('href');
        if (id.length < 2) return;
        var target = document.querySelector(id);
        if (!target) return;
        e.preventDefault();
        var navH = 64;
        var y = target.getBoundingClientRect().top + window.scrollY - navH;
        window.scrollTo({ top: y, behavior: reduceMotion ? 'auto' : 'smooth' });
      });
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
  function boot() {
    initReveal();
    detectPlatform();
    initNavShadow();
    initSmoothScroll();
  }
})();
