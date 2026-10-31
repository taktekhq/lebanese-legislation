/* Lebanese Legislation: citation copying, the French text switch, and the
   table of contents following the reader. Shared behaviour is in main.js. */
(function () {
  "use strict";

  var toast;
  function say(msg) {
    if (!toast) {
      toast = document.createElement("div");
      toast.className = "toast";
      toast.setAttribute("role", "status");
      document.body.appendChild(toast);
    }
    toast.textContent = msg;
    toast.setAttribute("data-show", "");
    clearTimeout(say.t);
    say.t = setTimeout(function () { toast.removeAttribute("data-show"); }, 1800);
  }

  /* Copy a citation: the button carries the text, the page's URL anchors it. */
  document.addEventListener("click", function (ev) {
    var btn = ev.target.closest("[data-cite]");
    if (!btn) return;
    var url = new URL(btn.getAttribute("data-href") || location.href, location.href).href;
    var text = btn.getAttribute("data-cite") + "\n" + url;
    (navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject())
      .then(function () { say("Citation copied"); }, function () { window.prompt("Copy the citation", text); });
  });

  /* French text on or off, remembered for this reader only. */
  var body = document.querySelector("[data-lawtext]");
  var toggle = document.querySelector("[data-fr-toggle]");
  if (body && toggle) {
    var hide = false;
    try { hide = localStorage.getItem("lebleg.hideFr") === "1"; } catch (e) {}
    var apply = function () {
      if (hide) body.setAttribute("data-hide-fr", ""); else body.removeAttribute("data-hide-fr");
      toggle.setAttribute("aria-pressed", hide ? "false" : "true");
      toggle.textContent = hide ? "Show French" : "Hide French";
    };
    apply();
    toggle.addEventListener("click", function () {
      hide = !hide;
      try { localStorage.setItem("lebleg.hideFr", hide ? "1" : "0"); } catch (e) {}
      apply();
    });
  }

  /* Highlight the chapter being read in the table of contents. */
  var links = document.querySelectorAll(".lawtoc a[href^='#']");
  if (links.length && "IntersectionObserver" in window) {
    var byId = {};
    links.forEach(function (a) { byId[a.getAttribute("href").slice(1)] = a; });
    var current;
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (!en.isIntersecting) return;
        var a = byId[en.target.id];
        if (!a || a === current) return;
        if (current) current.removeAttribute("aria-current");
        a.setAttribute("aria-current", "true");
        current = a;
      });
    }, { rootMargin: "-90px 0px -70% 0px" });
    Object.keys(byId).forEach(function (id) {
      var el = document.getElementById(id);
      if (el) io.observe(el);
    });
  }
})();

/* Go to an article by its number. Accepts Arabic-Indic digits (٢٣), "مكرر"
   in any spacing and "bis"; an exact number wins, else the first article
   whose number starts with those digits. Without JavaScript the form goes to
   /law/{id}/go/?n=…, which redirects to the same place. */
(function () {
  "use strict";
  var form = document.querySelector("[data-goto]");
  if (!form) return;
  var input = form.querySelector("input"), msg = form.querySelector("[data-goto-msg]");
  var digits = { "٠": "0", "١": "1", "٢": "2", "٣": "3", "٤": "4", "٥": "5", "٦": "6", "٧": "7", "٨": "8", "٩": "9",
                 "۰": "0", "۱": "1", "۲": "2", "۳": "3", "۴": "4", "۵": "5", "۶": "6", "۷": "7", "۸": "8", "۹": "9" };
  function norm(s) {
    s = String(s).replace(/[٠-٩۰-۹]/g, function (d) { return digits[d]; });
    s = s.replace(/^\s*(ال)?ماد[ةه]\s*/, "").replace(/(^|\d|\s)bis\b/gi, "$1مكرر").replace(/\s*-\s*/g, " ");
    s = s.replace(/مكرر\s*(\d+)/g, "مكرر $1").replace(/(\d)\s*مكرر/g, "$1 مكرر");
    return s.replace(/\s+/g, " ").trim();
  }
  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var want = norm(input.value);
    if (!want) return;
    var arts = document.querySelectorAll(".art[data-n]"), hit = null;
    for (var i = 0; i < arts.length && !hit; i++) if (norm(arts[i].getAttribute("data-n")) === want) hit = arts[i];
    var lead = (want.match(/^\d+/) || [])[0];
    for (var j = 0; j < arts.length && !hit && lead; j++) {
      if ((norm(arts[j].getAttribute("data-n")).match(/^\d+/) || [])[0] === lead) hit = arts[j];
    }
    if (!hit && form.hasAttribute("data-split")) { form.submit(); return; } /* a long text read in parts: the server finds the article's page */
    if (!hit) {
      msg.textContent = "لا توجد في هذا النص مادة رقمها " + want + ".";
      msg.hidden = false;
      return;
    }
    msg.hidden = true;
    history.replaceState(null, "", "#" + hit.id);
    hit.scrollIntoView({ behavior: "smooth", block: "start" });
    hit.removeAttribute("data-flash");
    void hit.offsetWidth;
    hit.setAttribute("data-flash", "");
    input.blur();
  });
})();
(function () {
  "use strict";
  var section = document.querySelector("[data-grounded-answer]");
  if (!section) return;
  var target = section.querySelector("[data-answer-content]");
  var body = new URLSearchParams({q: section.getAttribute("data-question")});
  var controller = new AbortController();
  var timeout = setTimeout(function () { controller.abort(); }, 28000);
  fetch(section.getAttribute("data-answer-url"), {
    method: "POST", credentials: "same-origin", body: body, signal: controller.signal
  }).then(function (response) {
    if (!response.ok) throw new Error("answer unavailable");
    return response.text();
  }).then(function (html) {
    target.innerHTML = html;
  }).catch(function () {
    target.textContent = section.getAttribute("data-error");
  }).finally(function () { clearTimeout(timeout); });
})();
