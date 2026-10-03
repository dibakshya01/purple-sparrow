// Purple Sparrow site — small, dependency-free interactions & motion.
(function () {
  "use strict";
  var REDUCE = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---- Mobile nav toggle ---- */
  var btn = document.querySelector(".menu-btn");
  var links = document.querySelector(".nav__links");
  if (btn && links) {
    btn.addEventListener("click", function () {
      links.classList.toggle("open");
      btn.setAttribute("aria-expanded", links.classList.contains("open"));
    });
  }

  /* ---- Copy-to-clipboard ---- */
  document.querySelectorAll("[data-copy]").forEach(function (el) {
    el.addEventListener("click", function () {
      var text = el.getAttribute("data-copy");
      navigator.clipboard && navigator.clipboard.writeText(text).then(function () {
        var old = el.textContent;
        el.textContent = "copied ✓";
        setTimeout(function () { el.textContent = old; }, 1400);
      });
    });
  });

  /* ---- Scroll progress bar + nav condense ---- */
  var nav = document.querySelector(".nav");
  var bar = document.getElementById("progress");
  var ticking = false;
  function onScroll() {
    if (ticking) return;
    ticking = true;
    requestAnimationFrame(function () {
      var st = window.pageYOffset || document.documentElement.scrollTop;
      var h = document.documentElement.scrollHeight - window.innerHeight;
      if (bar) bar.style.width = (h > 0 ? (st / h) * 100 : 0) + "%";
      if (nav) nav.classList.toggle("scrolled", st > 8);
      ticking = false;
    });
  }
  window.addEventListener("scroll", onScroll, { passive: true });
  onScroll();

  /* ---- Staggered reveal-on-scroll ---- */
  var items = document.querySelectorAll(".reveal");
  items.forEach(function (el) {
    var parent = el.parentNode;
    var sibs = Array.prototype.filter.call(parent.children, function (c) {
      return c.classList && c.classList.contains("reveal");
    });
    var idx = sibs.indexOf(el);
    if (idx > 0 && !REDUCE) el.style.transitionDelay = Math.min(idx * 0.08, 0.4) + "s";
  });
  if ("IntersectionObserver" in window && items.length) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
      });
    }, { threshold: 0.12 });
    items.forEach(function (i) { io.observe(i); });
  } else {
    items.forEach(function (i) { i.classList.add("in"); });
  }

  /* ---- Hero: pointer-reactive spotlight + grid parallax ---- */
  var hero = document.querySelector(".hero");
  var spot = document.querySelector(".hero__spot");
  var grid = document.querySelector(".hero__grid");
  if (hero && spot && !REDUCE) {
    var raf = null, lx = 50, ly = 32;
    hero.addEventListener("pointermove", function (ev) {
      var r = hero.getBoundingClientRect();
      lx = ((ev.clientX - r.left) / r.width) * 100;
      ly = ((ev.clientY - r.top) / r.height) * 100;
      if (!raf) raf = requestAnimationFrame(function () {
        raf = null;
        spot.style.setProperty("--mx", lx + "%");
        spot.style.setProperty("--my", ly + "%");
        if (grid) {
          var gx = (lx - 50) / 50 * 8, gy = (ly - 50) / 50 * 8;
          grid.style.transform = "translate(" + (-gx).toFixed(1) + "px," + (-gy).toFixed(1) + "px)";
        }
      });
    });
    hero.addEventListener("pointerleave", function () { if (grid) grid.style.transform = ""; });
  }

  /* ---- Cards: cursor spotlight ---- */
  if (!REDUCE) {
    document.querySelectorAll(".card").forEach(function (card) {
      card.addEventListener("pointermove", function (ev) {
        var r = card.getBoundingClientRect();
        card.style.setProperty("--cx", (ev.clientX - r.left) + "px");
        card.style.setProperty("--cy", (ev.clientY - r.top) + "px");
      });
    });
  }

  /* ---- Interactive demo: "watch an agent recover" ---- */
  var screen = document.getElementById("demo-screen");
  var replay = document.getElementById("demo-replay");
  if (screen) {
    var steps = [
      {
        req: "GET /v1/tables/todos/records",
        pill: ["deny", "403 · policy_denied"],
        res: '{\n  <span class="k">"error"</span>: {\n    <span class="k">"code"</span>:         <span class="s">"policy_denied"</span>,\n    <span class="k">"message"</span>:      <span class="s">"no policy grants select on todos"</span>,\n    <span class="k">"remediation"</span>:  <span class="s">"grant a select policy for this role"</span>,\n    <span class="k">"next_actions"</span>: [<span class="s">"POST /v1/policies"</span>]\n  }\n}',
        cap: '<span class="arrow">→</span><span><b>It hit a wall.</b> The error carries the fix, not just a code.</span>'
      },
      {
        req: 'POST /v1/policies  {"table":"todos","action":"select","roles":["authenticated"],"using":"auth.uid() = owner_id"}',
        pill: ["ok", "201 · created"],
        res: '{ <span class="k">"id"</span>: <span class="s">"pol_7f3a2c"</span>, <span class="k">"status"</span>: <span class="s">"active"</span> }',
        cap: '<span class="arrow">→</span><span><b>It applied the remediation</b> — owner-scoped, deny-by-default.</span>'
      },
      {
        req: "GET /v1/tables/todos/records",
        pill: ["ok", "200 · ok"],
        res: '{ <span class="k">"records"</span>: [\n  { <span class="k">"id"</span>: <span class="s">"rec_91b0"</span>, <span class="k">"title"</span>: <span class="s">"ship it"</span>, <span class="k">"owner_id"</span>: <span class="s">"me"</span> }\n] }',
        cap: '<span class="arrow">→</span><span><b>Recovered on its own.</b> No human touched the backend.</span>'
      }
    ];

    var runId = 0;
    var timers = [];
    function clearTimers() { timers.forEach(clearTimeout); timers = []; }
    function wait(ms) { return new Promise(function (res) { timers.push(setTimeout(res, ms)); }); }

    function nodeFor(step) {
      var el = document.createElement("div");
      el.className = "demo__step";
      el.innerHTML =
        '<div class="demo__req"><span class="mark">›</span><span class="typed"></span></div>' +
        '<div class="demo__res"><span class="demo__status"><span class="pill pill--' + step.pill[0] + '">' + step.pill[1] + "</span></span>" + step.res + "</div>" +
        '<div class="demo__cap">' + step.cap + "</div>";
      return el;
    }

    function typeText(target, text, myRun) {
      return new Promise(function (resolve) {
        var i = 0;
        var caret = document.createElement("span");
        caret.className = "caret";
        target.appendChild(caret);
        (function tick() {
          if (myRun !== runId) return;
          if (i <= text.length) {
            target.textContent = text.slice(0, i);
            target.appendChild(caret);
            i++;
            timers.push(setTimeout(tick, 14 + Math.random() * 22));
          } else {
            caret.remove();
            resolve();
          }
        })();
      });
    }

    function play() {
      runId++;
      var myRun = runId;
      clearTimers();
      screen.innerHTML = "";
      if (REDUCE) {
        steps.forEach(function (s) {
          var n = nodeFor(s);
          n.classList.add("answered");
          n.querySelector(".typed").textContent = s.req;
          screen.appendChild(n);
        });
        return;
      }
      (function run(k) {
        if (myRun !== runId || k >= steps.length) return;
        var node = nodeFor(steps[k]);
        screen.appendChild(node);
        typeText(node.querySelector(".typed"), steps[k].req, myRun).then(function () {
          if (myRun !== runId) return;
          return wait(300);
        }).then(function () {
          if (myRun !== runId) return;
          node.classList.add("answered");
          return wait(1150);
        }).then(function () {
          run(k + 1);
        });
      })(0);
    }

    if ("IntersectionObserver" in window && !REDUCE) {
      var started = false;
      var dio = new IntersectionObserver(function (es) {
        es.forEach(function (e) {
          if (e.isIntersecting && !started) { started = true; play(); }
        });
      }, { threshold: 0.35 });
      dio.observe(document.getElementById("demo"));
    } else {
      play();
    }
    if (replay) replay.addEventListener("click", play);
  }
})();
