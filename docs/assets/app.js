// Orange Crow site — small, dependency-free interactions.
(function () {
  // Mobile nav toggle.
  var btn = document.querySelector('.menu-btn');
  var links = document.querySelector('.nav__links');
  if (btn && links) {
    btn.addEventListener('click', function () {
      links.classList.toggle('open');
      btn.setAttribute('aria-expanded', links.classList.contains('open'));
    });
  }

  // Copy-to-clipboard buttons.
  document.querySelectorAll('[data-copy]').forEach(function (el) {
    el.addEventListener('click', function () {
      var text = el.getAttribute('data-copy');
      navigator.clipboard && navigator.clipboard.writeText(text).then(function () {
        var old = el.textContent;
        el.textContent = 'copied';
        setTimeout(function () { el.textContent = old; }, 1400);
      });
    });
  });

  // Reveal-on-scroll (respects reduced motion via CSS fallback).
  var items = document.querySelectorAll('.reveal');
  if ('IntersectionObserver' in window && items.length) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); }
      });
    }, { threshold: 0.12 });
    items.forEach(function (i) { io.observe(i); });
  } else {
    items.forEach(function (i) { i.classList.add('in'); });
  }
})();
