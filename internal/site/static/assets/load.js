/* Analytics after the page, not before it. gtag.js (Google Analytics) and
   PostHog are about 700 KB between them and nothing on the page waits for
   them, so they are fetched on the first touch, scroll, key press or click,
   or two seconds after the page has loaded (when the browser is idle), if the
   visitor does nothing sooner. A visit is still counted: ga.js has already
   queued the page view in dataLayer, and PostHog replays what was queued. What
   they capture is unchanged; this only decides when they are fetched. */
(function () {
  "use strict";
  var ph = document.currentScript && document.currentScript.getAttribute("data-ph");
  var started = false;

  /* Named events (search, cite_copy…) are sent by ga.js's track() to PostHog
     too. Until ph.js has run, give it somewhere to put them: PostHog's own
     snippet adopts an existing array and replays what is in it. */
  if (ph && !window.posthog) {
    var queue = (window.posthog = []);
    queue.capture = function () { queue.push(["capture"].concat([].slice.call(arguments))); };
  }

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
    add("https://www.googletagmanager.com/gtag/js?id=G-RDYRK422NP", true);
    if (ph) add(ph);
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
