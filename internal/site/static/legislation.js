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

  /* "Was this answer helpful?": the answer brings it, and it moves below the
     box so a collapsed answer can still be rated. Ratings and notes go to
     PostHog only, with the question and the answer they are about; our
     server never stores them. */
  function placeRating() {
    box.querySelectorAll(":scope > [data-ai-rate]").forEach(function (el) { el.remove(); });
    var rate = target.querySelector("[data-ai-rate]");
    if (rate) box.append(rate);
  }
  function rated(rate) {
    var text = target.querySelector(".ai-box__text");
    return {
      question: section.getAttribute("data-q"),
      mode: rate.getAttribute("data-mode"),
      answer: text ? text.innerText.slice(0, 3000) : "",
      sources: [].map.call(target.querySelectorAll(".ai-box__src"), function (a) { return a.getAttribute("href"); })
    };
  }
  box.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-rate]");
    if (!btn || btn.getAttribute("aria-pressed") === "true") return;
    var rate = btn.closest("[data-ai-rate]");
    var value = btn.getAttribute("data-rate");
    rate.querySelectorAll("[data-rate]").forEach(function (b) { b.setAttribute("aria-pressed", String(b === btn)); });
    var props = rated(rate);
    props.rating = value;
    props.changed = rate.hasAttribute("data-rated");
    track("ai_answer_rated", props);
    rate.setAttribute("data-rated", "");
    var why = rate.querySelector("[data-ai-why]");
    var thanks = rate.querySelector("[data-ai-thanks]");
    why.hidden = value !== "down";
    thanks.textContent = value === "down" ? "" : rate.getAttribute("data-thanks");
  });
  /* After a thumbs down: one tap on a reason sends it; "Something else"
     opens a box for their own words. */
  function noted(rate, reason, note) {
    var props = rated(rate);
    props.rating = "down";
    props.reason = reason;
    props.note = note.slice(0, 500);
    track("ai_answer_note", props);
  }
  /* the thanks line is a live region that is always there, so a screen
     reader hears it; focus moves (to the thumb, or to what the reply points
     at) before the reasons go */
  function thank(rate, said, focus) {
    (focus || rate.querySelector('[data-rate="down"]')).focus();
    rate.querySelector("[data-ai-why]").hidden = true;
    /* reset, so a later thumbs down starts from the reasons again */
    var form = rate.querySelector("[data-ai-note]");
    form.hidden = true;
    form.querySelector("textarea").value = "";
    rate.querySelector('[data-reason="other"]').setAttribute("aria-expanded", "false");
    rate.querySelector("[data-ai-thanks]").textContent = rate.getAttribute(said);
  }
  /* Answer the reason where it was given: a wrong law shows the laws the
     answer used, out of date shows when each was last updated and amended,
     no answer points to the deep answer when one is left. */
  function openBox() {
    var more = box.querySelector(".ai-box__more");
    if (more && box.classList.contains("ai-box--collapsed")) more.click();
  }
  function reply(reason) {
    var list = target.querySelector(".ai-box__sources");
    var first = list && list.querySelector(".ai-box__src");
    if (reason === "wrong_law" && first) {
      openBox();
      [].forEach.call(list.children, function (li) { li.classList.add("ai-box__flash"); });
      return { said: "data-reply-wrong_law", focus: first };
    }
    var dates = target.querySelectorAll("[data-ai-updated]");
    if (reason === "out_of_date" && dates.length && first) {
      openBox();
      [].forEach.call(dates, function (d) { d.hidden = false; });
      return { said: "data-reply-out_of_date", focus: first };
    }
    var deep = target.querySelector("[data-ai-deep]:not([disabled])");
    if (reason === "no_answer" && deep) {
      openBox();
      return { said: "data-reply-no_answer", focus: deep };
    }
    return { said: "data-reason-sent" };
  }
  box.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-reason]");
    if (!btn) return;
    var rate = btn.closest("[data-ai-rate]");
    var reason = btn.getAttribute("data-reason");
    if (reason !== "other") {
      noted(rate, reason, "");
      var r = reply(reason);
      thank(rate, r.said, r.focus);
      return;
    }
    var form = rate.querySelector("[data-ai-note]");
    form.hidden = !form.hidden;
    btn.setAttribute("aria-expanded", String(!form.hidden));
    /* focus the box only with a mouse: on a phone it would zoom and open
       the keyboard before they chose to type */
    if (!form.hidden && matchMedia("(hover: hover)").matches) form.querySelector("textarea").focus();
  });
  box.addEventListener("submit", function (e) {
    var form = e.target.closest("[data-ai-note]");
    if (!form) return;
    e.preventDefault();
    var rate = form.closest("[data-ai-rate]");
    var note = form.querySelector("textarea").value.trim();
    if (note) noted(rate, "other", note);
    thank(rate, note ? "data-sent" : "data-thanks");
  });

  function settle() {
    box.removeAttribute("aria-busy");
    if (wait) wait.remove();
  }
  /* PostHog loads after the first touch or two idle seconds (assets/load.js),
     usually after this runs, so look it up on every call: until then
     window.posthog is load.js's queue, and ph.js sends what is in it. */
  function track(name, props) {
    var ph = window.posthog;
    if (ph && ph.capture) ph.capture(name, props);
  }
  /* The visitor's PostHog ID, so the server's ai_question joins their
     session: from PostHog once it has loaded, else from the cookie it kept
     on an earlier page. A first page has neither and sends none. */
  function visitorID() {
    var ph = window.posthog;
    try { if (ph && ph.__loaded && ph.get_distinct_id) return ph.get_distinct_id() || ""; } catch (e) {}
    try {
      var m = document.cookie.match(/(?:^|;\s*)ph_phc_[A-Za-z0-9]+_posthog=([^;]*)/);
      if (m) return JSON.parse(decodeURIComponent(m[1])).distinct_id || "";
    } catch (e) {}
    return "";
  }
  var body = new URLSearchParams({q: section.getAttribute("data-q"), empty: section.getAttribute("data-empty") || ""});
  var visitor = visitorID();
  if (visitor) body.set("ph", visitor);
  var controller = new AbortController();
  var timeout = setTimeout(function () { controller.abort(); }, 30000);
  fetch(section.getAttribute("data-url"), {
    method: "POST", credentials: "same-origin", body: body, signal: controller.signal
  }).then(function (response) {
    if (!response.ok) throw new Error("answer unavailable");
    return response.text();
  }).then(function (html) {
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
    placeRating();
    var text = target.querySelector(".ai-box__text");
    track(text ? "ai_answer_shown" : target.querySelector(".ai-box__limit") ? "ai_limit_shown" : "ai_no_answer_shown",
      { sources: target.querySelectorAll(".ai-box__src").length, brought_results: !!results });
    target.addEventListener("click", function (e) {
      var a = e.target.closest(".ai-box__src");
      if (a) track("ai_source_clicked", { href: a.getAttribute("href"), position: Number((a.querySelector(".ai-box__n") || {}).textContent) || 0 });
    });
    target.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-ai-deep]");
      if (!btn) return;
      btn.disabled = true;
      track("ai_deep_requested", {});
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
      placeRating();
      var dc = new AbortController();
      var dt = setTimeout(function () { dc.abort(); }, 90000);
      fetch(section.getAttribute("data-deep-url"), { method: "POST", credentials: "same-origin", body: deepBody, signal: dc.signal })
        .then(function (r) { if (!r.ok) throw new Error("deep answer unavailable"); return r.text(); })
        .then(function (html) {
          var t2 = document.createElement("template");
          t2.innerHTML = html;
          target.textContent = "";
          target.append(t2.content);
          placeRating();
          track(target.querySelector(".ai-box__text") ? "ai_deep_shown" : "ai_deep_no_answer", { sources: target.querySelectorAll(".ai-box__src").length });
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
      if (open) track("ai_answer_expanded", {});
    });
    box.insertBefore(more, box.querySelector(":scope > [data-ai-rate]"));
    /* a citation number opens the box before jumping to its source */
    target.addEventListener("click", function (e) {
      if (e.target.closest(".ai-box__ref") && box.classList.contains("ai-box--collapsed")) more.click();
    });
  }).catch(function () {
    target.textContent = section.getAttribute("data-error");
    if (fallback) fallback.hidden = false;
    settle();
  }).finally(function () { clearTimeout(timeout); });
})();

/* Editors and error reports.

   An editor (a paid student reviewer) gets a personal link that ends in
   ?editor=CODE. The code, not a name, is kept in this browser and ph.js sends
   it with every PostHog event, so their study days and reports can be counted.
   The address is cleaned so the code is not shared with the page.

   "Report an error on this page" in the footer sends the note to PostHog as
   error_report (with the page, the language and the editor code if any).
   Only PostHog gets the text: GA4 must not hold free text. */
(function () {
  "use strict";
  try {
    var m = location.search.match(/[?&]editor=([A-Za-z0-9-]{3,24})(?=&|$)/);
    if (m) {
      localStorage.setItem("lebleg_editor", m[1].toLowerCase());
      var rest = location.search.replace(/([?&])editor=[^&]*&?/, "$1").replace(/[?&]$/, "");
      history.replaceState(null, "", location.pathname + rest + location.hash);
    }
  } catch (e) {}

  var box = document.querySelector("[data-report]");
  if (!box) return;
  var open = box.querySelector("[data-report-open]");
  var form = box.querySelector("[data-report-form]");
  var done = box.querySelector("[data-report-done]");
  open.addEventListener("click", function () {
    var show = form.hidden;
    form.hidden = !show;
    done.hidden = true;
    open.setAttribute("aria-expanded", String(show));
    if (show && matchMedia("(hover: hover)").matches) form.querySelector("textarea").focus();
  });
  form.addEventListener("submit", function (e) {
    e.preventDefault();
    var note = form.querySelector("textarea").value.trim();
    if (!note) return;
    var ph = window.posthog;
    var sent = !!(ph && ph.capture);
    if (sent) {
      ph.capture("error_report", {
        note: note.slice(0, 1000),
        page: location.pathname,
        language: document.documentElement.lang === "ar" ? "ar" : "en"
      });
      form.querySelector("textarea").value = "";
      form.hidden = true;
      open.setAttribute("aria-expanded", "false");
    }
    done.textContent = done.getAttribute(sent ? "data-thanks" : "data-failed");
    done.hidden = false;
  });
})();
