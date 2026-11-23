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
  var input = form.querySelector('input[name="n"]'), msg = form.querySelector("[data-goto-msg]");
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
    /* Smooth scrolling lays out skipped articles along the way and moves
       the destination in long texts using content-visibility: auto. */
    hit.scrollIntoView({ behavior: "instant", block: "start" });
    hit.removeAttribute("data-flash");
    void hit.offsetWidth;
    hit.setAttribute("data-flash", "");
    input.blur();
  });
})();
(function () {
  "use strict";
  /* The AI answer box at the top of a search that reads as a question. It
     starts collapsed like a search engine's overview, and, when the page's
     own search found nothing, also brings the texts the AI search found. */
  var section = document.querySelector("[data-ai-answer]");
  if (!section) return;
  var box = section.querySelector(".ai-box");
  var target = section.querySelector("[data-ai-content]");
  var fallback = document.querySelector("[data-ai-fallback]");
  var wait = null;
  if (fallback && section.getAttribute("data-empty") === "1") {
    fallback.hidden = true;
    wait = document.createElement("p");
    wait.className = "tiny";
    wait.style.marginTop = "28px";
    wait.textContent = section.getAttribute("data-finding");
    fallback.parentNode.insertBefore(wait, fallback);
  }

  function settle() {
    box.removeAttribute("aria-busy");
    if (wait) wait.remove();
  }
  var body = new URLSearchParams({q: section.getAttribute("data-q"), empty: section.getAttribute("data-empty") || ""});
  /* A reader who opens a cited source and comes back (Back button) gets the
     answer they already had from this tab's sessionStorage: no second
     question to the server, none of their daily questions spent. Keyed by
     question, mode (the URL) and language; answers only, never a limit or
     error message. Storage can be off or full, so every use is guarded.
     A page restored from the back/forward cache never runs this again. */
  var saveKey = "lebleg_ai:" + (document.documentElement || {}).lang + ":" + section.getAttribute("data-url") + ":" +
    (section.getAttribute("data-empty") || "") + ":" + section.getAttribute("data-q");
  var saved = null;
  try { saved = window.sessionStorage.getItem(saveKey); } catch (e) {}
  if (saved) {
    show(saved, true);
    return;
  }
  var controller = new AbortController();
  var timeout = setTimeout(function () { controller.abort(); }, 30000);
  fetch(section.getAttribute("data-url"), {
    method: "POST", credentials: "same-origin", body: body, signal: controller.signal
  }).then(function (response) {
    if (!response.ok) throw new Error("answer unavailable");
    return response.text();
  }).then(function (html) {
    show(html, false);
  }).catch(function () {
    target.textContent = section.getAttribute("data-error");
    if (fallback) fallback.hidden = false;
    settle();
  }).finally(function () { clearTimeout(timeout); });

  /* show puts an answer (fresh, or restored from this tab) in the box. */
  function show(html, restored) {
    var tpl = document.createElement("template");
    tpl.innerHTML = html;
    var results = tpl.content.querySelector("[data-ai-results]");
    if (results) results.remove();
    target.textContent = "";
    target.append(tpl.content);
    if (results && fallback) {
      fallback.replaceWith(results);
      fallback = null;
    }
    if (fallback) fallback.hidden = false;
    settle();
    var text = target.querySelector(".ai-box__text");
    if (text && !restored) {
      try { window.sessionStorage.setItem(saveKey, html); } catch (e) {}
    }
    target.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-ai-deep]");
      if (!btn) return;
      btn.disabled = true;
      var deepBody = new URLSearchParams(body);
      deepBody.delete("empty");
      box.classList.remove("ai-box--collapsed");
      var oldMore = box.querySelector(".ai-box__more");
      if (oldMore) oldMore.remove();
      box.setAttribute("aria-busy", "true");
      var wait = document.createElement("p");
      wait.className = "tiny";
      wait.textContent = section.getAttribute("data-deep-loading");
      target.textContent = "";
      target.append(wait);
        var dc = new AbortController();
      var dt = setTimeout(function () { dc.abort(); }, 90000);
      fetch(section.getAttribute("data-deep-url"), { method: "POST", credentials: "same-origin", body: deepBody, signal: dc.signal })
        .then(function (r) { if (!r.ok) throw new Error("deep answer unavailable"); return r.text(); })
        .then(function (html) {
          var t2 = document.createElement("template");
          t2.innerHTML = html;
          target.textContent = "";
          target.append(t2.content);
              })
        .catch(function () { target.textContent = section.getAttribute("data-error"); })
        .finally(function () { clearTimeout(dt); box.removeAttribute("aria-busy"); });
    });
    if (!text) return;
    box.classList.add("ai-box--collapsed");
    if (target.scrollHeight <= target.clientHeight + 24) {
      box.classList.remove("ai-box--collapsed");
      return;
    }
    var more = document.createElement("button");
    more.type = "button";
    more.className = "ai-box__more";
    more.setAttribute("aria-expanded", "false");
    more.textContent = section.getAttribute("data-more");
    more.addEventListener("click", function () {
      var open = box.classList.toggle("ai-box--collapsed") === false;
      more.setAttribute("aria-expanded", String(open));
      more.textContent = section.getAttribute(open ? "data-less" : "data-more");
    });
    box.appendChild(more);
    /* a citation number opens the box before jumping to its source */
    target.addEventListener("click", function (e) {
      if (e.target.closest(".ai-box__ref") && box.classList.contains("ai-box--collapsed")) more.click();
    });
  }
})();

/* "Show more" under a part of the search results: the link's own page lists
   the next rows (it works without this script); here they are appended under
   the list instead, and the link is replaced by the one for the page after. */
(function () {
  "use strict";
  if (typeof fetch !== "function" || typeof DOMParser !== "function") return;
  document.addEventListener("click", function (ev) {
    var a = ev.target.closest && ev.target.closest("a[data-more]");
    if (!a || ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.altKey || ev.button) return;
    var wrap = a.closest("[data-more-wrap]");
    var list = wrap && wrap.previousElementSibling;
    if (!list || !list.classList.contains("doclist")) return;
    ev.preventDefault();
    if (a.getAttribute("aria-busy") === "true") return;
    a.setAttribute("aria-busy", "true");
    fetch(a.href + "&frag=1", { credentials: "same-origin" }).then(function (r) {
      if (!r.ok) throw new Error(String(r.status));
      return r.text();
    }).then(function (html) {
      var doc = new DOMParser().parseFromString(html, "text/html");
      var rows = doc.querySelectorAll("ul.doclist > li");
      var first = null;
      [].forEach.call(rows, function (li) {
        var row = document.importNode(li, true);
        if (!first) first = row.querySelector("a");
        list.appendChild(row);
      });
      var next = doc.querySelector("[data-more-wrap]");
      if (next && rows.length) wrap.parentNode.replaceChild(document.importNode(next, true), wrap);
      else wrap.parentNode.removeChild(wrap);
      if (first) first.focus({ preventScroll: true });
    }).catch(function () {
      location.href = a.href;
    });
  });
})();
