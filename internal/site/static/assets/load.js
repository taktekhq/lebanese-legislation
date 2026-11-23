/* Analytics after the page, not before it. Nothing on the page waits for
   gtag.js (Google Analytics), so it is fetched on the first touch, scroll, key press or click,
   or two seconds after the page has loaded (when the browser is idle), if the
   visitor does nothing sooner. A visit is still counted: ga.js has already
   queued the page view in dataLayer. What it captures is unchanged; this only
   decides when it is fetched. */
(function () {
  "use strict";
  var started = false;

  function add(src, async) {
    var s = document.createElement("script");
    s.src = src;
    if (async) s.async = true;
    document.head.appendChild(s);
  }

  function start() {
    if (started) return;
    started = true;
    ["pointerdown", "keydown", "scroll", "touchstart", "mousemove"].forEach(function (e) {
      window.removeEventListener(e, start, { passive: true });
    });
    add("/gtag/js?id=G-RDYRK422NP", true);
  }

  ["pointerdown", "keydown", "scroll", "touchstart", "mousemove"].forEach(function (e) {
    window.addEventListener(e, start, { passive: true, once: true });
  });
  function later() {
    if ("requestIdleCallback" in window) requestIdleCallback(start, { timeout: 2000 });
    else setTimeout(start, 2000);
  }
  if (document.readyState === "complete") later();
  else window.addEventListener("load", later);
})();
