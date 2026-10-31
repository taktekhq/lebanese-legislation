/* ==========================================================================
   Lebanese Legislation
   --------------------------------------------------------------------------
   The Lebanese family's script, as copied into Lebanese Businesses (which added
   lockup, chips, listings and tiles to FIELDS), so every family site behaves
   as one family. The law pages' own behaviour is in legislation.js.
   ========================================================================== */

const CONFIG = {
  email: "",
};

/* --------------------------------------------------------------------------
   Nothing below needs editing.
   -------------------------------------------------------------------------- */

(function () {
  "use strict";

  /* --- Wire up the address ---------------------------------------------- */

  if (CONFIG.email) {
    document.querySelectorAll('a[href^="mailto:"]').forEach(function (el) {
      var shown = (el.textContent || "").trim();
      el.href = el.href.replace(/mailto:[^?]*/, "mailto:" + CONFIG.email);
      /* The footer prints the address rather than a label, so keep it in step. */
      if (shown.indexOf("@") > -1) el.textContent = CONFIG.email;
    });
  }

  /* --- Where did they come from ------------------------------------------
     Analytics cannot see AI crawlers: GA4 runs in JavaScript and crawlers do
     not execute it. What it can see is a person arriving from an AI answer,
     which is the number worth watching anyway. Tag those sessions so they are
     one filter away rather than buried in referral noise. */

  /* --- Sticky nav background -------------------------------------------- */

  var nav = document.getElementById("nav");

  if (nav) {
    var setStuck = function () {
      nav.dataset.stuck = window.scrollY > 8 ? "true" : "false";
    };
    setStuck();
    window.addEventListener("scroll", setStuck, { passive: true });
  }

  /* --- Reveal on scroll -------------------------------------------------- */

  var targets = document.querySelectorAll("[data-reveal]");
  var reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  if (reduced || !("IntersectionObserver" in window)) {
    targets.forEach(function (el) {
      el.classList.add("is-in");
    });
  } else {
    var observer = new IntersectionObserver(
      function (entries) {
        entries.forEach(function (entry) {
          if (!entry.isIntersecting) return;
          entry.target.classList.add("is-in");
          observer.unobserve(entry.target);
        });
      },
      { rootMargin: "0px 0px -8% 0px", threshold: 0.08 }
    );

    targets.forEach(function (el) {
      observer.observe(el);
    });

    /* Anything already on screen at load fires immediately, so the hero does
       not wait for a scroll that may never come, including when the page is
       opened directly on an #anchor. */
    var revealVisible = function () {
      targets.forEach(function (el) {
        var top = el.getBoundingClientRect().top;
        if (top < window.innerHeight && top > -el.offsetHeight) {
          el.classList.add("is-in");
        }
      });
    };
    requestAnimationFrame(revealVisible);
    window.addEventListener("load", revealVisible);

    /* Failsafe. If the observer never fires for any reason, the page must not
       sit there blank. Show everything after two seconds regardless. */
    setTimeout(function () {
      targets.forEach(function (el) {
        el.classList.add("is-in");
      });
    }, 2000);
  }

  /* --- Cursor ------------------------------------------------------------
     A gravity field, after robbietilton.com.

     Every attractor projects a field that reaches `radius` pixels past its own
     edge. Inside it the element leans toward the pointer by up to `pull` px,
     scaled by how close the pointer is. The pointer itself is the system
     cursor: this site does not draw one.

     Distance is measured to the nearest point on the element's box, not its
     centre, so a wide card does not yank the cursor across the screen and the
     field feels the same depth on every side. */

  var finePointer =
    window.matchMedia("(pointer: fine)").matches &&
    !window.matchMedia("(hover: none)").matches;

  /* The system cursor stays. Only the gravity is kept: targets lean toward the
     pointer. grab and dissolve are unused here; they drove the glass cursor
     the other family sites still draw, and stay so FIELDS reads the same. */
  if (finePointer) {
    var FIELDS = [
      /* Buttons sit 12px apart in a row. Their reach is deliberately short so
         two neighbours cannot both lunge into the gap and collide. Cards are
         far apart and large, so they can afford a much wider, stronger field. */
      { sel: ".btn",          pull: 10, grab: 0.55, radius: 20, dissolve: true },
      { sel: ".nav__links a", pull: 7,  grab: 0.55, radius: 22, dissolve: true },
      { sel: ".nav__out",     pull: 7,  grab: 0.55, radius: 22, dissolve: true },
      { sel: ".subnav__links a", pull: 6, grab: 0.5, radius: 20, dissolve: true },
      { sel: ".wordmark",     pull: 7,  grab: 0.5,  radius: 22, dissolve: true },
      { sel: ".lockup",       pull: 7,  grab: 0.5,  radius: 22, dissolve: true },
      { sel: ".chip",         pull: 8,  grab: 0.55, radius: 22, dissolve: true },
      { sel: ".social",       pull: 8,  grab: 0.55, radius: 22, dissolve: true },
      { sel: ".back",         pull: 7,  grab: 0.55, radius: 22, dissolve: true },
      { sel: ".sec__more",    pull: 7,  grab: 0.55, radius: 22, dissolve: true },
      { sel: ".item",         pull: 22, grab: 0.5,  radius: 60, dissolve: true },
      { sel: ".tile",         pull: 22, grab: 0.5,  radius: 60, dissolve: true },
      { sel: ".pitch",        pull: 5,  grab: 0.4,  radius: 32, dissolve: false },
      { sel: ".footer ul a",  pull: 7,  grab: 0.5,  radius: 22, dissolve: true },
      { sel: ".link",         pull: 5,  grab: 0.5,  radius: 20, dissolve: true },
      { sel: ".faq__q",       pull: 0,  grab: 0.45, radius: 26, dissolve: true },
      { sel: ".faq__item",    pull: 8,  grab: 0.45, radius: 44, dissolve: false },
      { sel: ".card--link",   pull: 22, grab: 0.5,  radius: 60, dissolve: true },
      { sel: ".card",         pull: 14, grab: 0.45, radius: 48, dissolve: false },
      { sel: ".ledger",       pull: 4,  grab: 0.35, radius: 30, dissolve: false },
      { sel: ".price",        pull: 4,  grab: 0.35, radius: 30, dissolve: false },
      { sel: ".band",         pull: 5,  grab: 0.4,  radius: 32, dissolve: false },
      { sel: ".pill",         pull: 5,  grab: 0.4,  radius: 24, dissolve: false },
    ];

    var bodies = [];

    /* The nav, and the bars that stick under it, float over the page. A card
       scrolled underneath must not reach through them: over a bar only that
       bar's own targets count, and everything else is measured by the part of
       it still showing below the bars. */
    var LAYERS = ".nav, .subnav, .finder";
    var layers = [].slice.call(document.querySelectorAll(LAYERS));

    /* An element can match more than one selector here: a service card is both
       .card--link and .card. Registering it twice gave it two configs writing
       to the same custom properties, each undoing the other, so the card never
       moved. First match wins. */
    var claimed = [];

    /* Callable again: the directory draws listings after load, and those need
       their fields too. Detached elements measure 0x0 and simply settle. */
    var claimAll = function () {
      FIELDS.forEach(function (f) {
        document.querySelectorAll(f.sel).forEach(function (el) {
          if (claimed.indexOf(el) > -1) return;
          claimed.push(el);
          /* The attribute is what the stylesheet hooks the transform onto. */
          if (f.pull) el.setAttribute("data-magnetic", "");
          bodies.push({ el: el, cfg: f, mx: 0, my: 0, layer: el.closest(LAYERS) });
        });
      });
      bodies = bodies.filter(function (b) { return b.el.isConnected; });
      claimed = claimed.filter(function (el) { return el.isConnected; });
    };
    claimAll();
    window.magnetize = claimAll;

    var x = 0;
    var y = 0;
    var queued = false;

    var clamp = function (v, lo, hi) {
      return v < lo ? lo : v > hi ? hi : v;
    };

    var settle = function (b) {
      if (b.mx === 0 && b.my === 0) return;
      b.mx = 0;
      b.my = 0;
      b.el.style.setProperty("--mx", "0px");
      b.el.style.setProperty("--my", "0px");
    };

    var step = function () {
      queued = false;


      /* Which bar the pointer is over, if any, and where the bars stuck to
         the top of the window end. A bar still in the flow covers nothing. */
      var over = null;
      var cover = 0;
      for (var j = 0; j < layers.length; j++) {
        var lr = layers[j].getBoundingClientRect();
        if (!lr.height) continue;
        if (x >= lr.left && x <= lr.right && y >= lr.top && y <= lr.bottom) over = layers[j];
        if (lr.top <= 61 && lr.bottom > cover) cover = lr.bottom;
      }

      for (var i = 0; i < bodies.length; i++) {
        var b = bodies[i];
        var r = b.el.getBoundingClientRect();

        /* The element is already displaced by its own pull, so undo that
           before measuring or the two feed each other. */
        var left = r.left - b.mx;
        var top = r.top - b.my;
        var right = left + r.width;
        var bottom = top + r.height;

        if (!b.layer && top < cover) top = cover;

        if (!r.width || bottom <= top || (over && b.layer !== over)) {
          settle(b);
          continue;
        }

        var nx = clamp(x, left, right);
        var ny = clamp(y, top, bottom);
        var dx = nx - x;
        var dy = ny - y;
        var dist = Math.sqrt(dx * dx + dy * dy);

        if (dist > b.cfg.radius) {
          settle(b);
          continue;
        }

        var t = 1 - dist / b.cfg.radius;

        if (b.cfg.pull) {
          var h = (bottom - top) / 2;
          var ox = clamp((x - (left + r.width / 2)) / (r.width / 2), -1, 1);
          var oy = clamp((y - (top + h)) / h, -1, 1);
          /* Squared falloff. Full strength under the pointer, but decaying
             fast outside the element, so neighbours in a row stop reaching
             into the same gap and knocking into each other. */
          var f = t * t;
          b.mx = ox * b.cfg.pull * f;
          b.my = oy * b.cfg.pull * f;
          b.el.style.setProperty("--mx", b.mx.toFixed(2) + "px");
          b.el.style.setProperty("--my", b.my.toFixed(2) + "px");
        }
      }
    };

    var schedule = function () {
      if (queued) return;
      queued = true;
      requestAnimationFrame(step);
    };

    document.addEventListener(
      "mousemove",
      function (e) {
        x = e.clientX;
        y = e.clientY;
        schedule();
      },
      { passive: true }
    );

    /* Scrolling moves the fields past a stationary pointer, so the whole thing
       has to be recomputed even when the mouse has not moved. */
    window.addEventListener("scroll", schedule, { passive: true });
    window.addEventListener("resize", schedule);
    document.addEventListener("click", schedule);

    document.addEventListener("mouseleave", function () {
      bodies.forEach(settle);
    });
  }

  /* --- FAQ --------------------------------------------------------------- */

  document.querySelectorAll(".faq__item").forEach(function (item, i) {
    var btn = item.querySelector(".faq__q");
    var panel = item.querySelector(".faq__a");
    if (!btn || !panel) return;

    var id = "faq-panel-" + i;
    panel.id = id;
    panel.setAttribute("role", "region");
    btn.setAttribute("aria-controls", id);

    btn.addEventListener("click", function () {
      var open = item.dataset.open === "true";

      /* One at a time. The section stays short enough to scan. */
      document.querySelectorAll('.faq__item[data-open="true"]').forEach(function (other) {
        other.dataset.open = "false";
        other.querySelector(".faq__q").setAttribute("aria-expanded", "false");
      });

      item.dataset.open = open ? "false" : "true";
      btn.setAttribute("aria-expanded", open ? "false" : "true");
    });
  });
})();
