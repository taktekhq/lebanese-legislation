// The static /search/ page: the site's own search form, and results drawn in
// the browser from the JSON API (GET /api/v1/search/). The page itself is
// plain HTML; nothing is rendered per request. data-api is the API's origin
// ("" for the same origin, which is how the edge routes /api/*).
(function () {
  "use strict";
  var me = document.currentScript;
  var api = ((me && me.getAttribute("data-api")) || "").replace(/\/+$/, "");
  var ar = document.documentElement.lang === "ar";
  var T = ar
    ? { laws: "النصوص", articles: "المواد", rulings: "الأحكام", gazette: "الجريدة الرسمية", none: "لا نتائج", busy: "جار البحث…", fail: "تعذر البحث الآن، حاول بعد قليل." }
    : { laws: "Texts", articles: "Articles", rulings: "Court rulings", gazette: "Gazette", none: "No results", busy: "Searching…", fail: "Search is unavailable right now, try again shortly." };
  var form = document.querySelector("form.search");
  if (!form) return;
  var box = document.createElement("section");
  box.className = "wrap";
  box.id = "search-results";
  box.setAttribute("aria-live", "polite");
  var host = form.closest("section") || form.parentNode;
  host.parentNode.insertBefore(box, host.nextSibling);

  function clean(s) { return String(s || "").replace(/[\u0002\u0003]/g, ""); }
  function href(u) {
    var p;
    try { p = new URL(u, location.origin).pathname; } catch (e) { return "#"; }
    return ar || !/^\/(law|decree|text)\//.test(p) ? p : "/en" + p;
  }
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }
  function section(title, count, items) {
    if (!items || !items.length) return;
    var h = el("h2", "sec__title", title + " ");
    h.appendChild(el("span", "muted", String(count)));
    box.appendChild(h);
    var ul = el("ul", "doclist");
    items.forEach(function (it) {
      var li = el("li");
      var a = el("a", "docrow");
      a.href = href(it.url);
      a.appendChild(el("span", "docrow__title", clean(it.title)));
      if (it.snippet) a.appendChild(el("span", "docrow__snip", clean(it.snippet)));
      li.appendChild(a);
      ul.appendChild(li);
    });
    box.appendChild(ul);
  }
  function show(r) {
    box.textContent = "";
    var arts = (r.articles || []).map(function (a) {
      return { url: a.url, title: (ar ? "المادة " : "Article ") + a.article_number + " · " + (a.law && a.law.title), snippet: a.snippet };
    });
    section(T.laws, r.law_count, r.laws);
    section(T.articles, r.article_count, arts);
    section(T.rulings, r.ruling_count, r.rulings);
    section(T.gazette, r.gazette_count, (r.gazette || []).map(function (g) { return { url: g.url, title: g.title || g.issue || "", snippet: g.snippet }; }));
    if (!box.firstChild) box.appendChild(el("p", "lead", T.none));
  }
  // A search that reads as a question also gets the AI answer box above the
  // results. The site's server decides (POST /search/ai/ answers 400 for a
  // lookup of a text), so nothing shows for "قانون العمل" or "44/2015".
  var aiBox = null, aiSeq = 0;
  function ai(q) {
    var seq = ++aiSeq;
    if (aiBox) { aiBox.remove(); aiBox = null; }
    if (!q) return;
    var body = new URLSearchParams({ q: q });
    var ph = window.posthog && window.posthog.get_distinct_id ? window.posthog : null;
    try { if (ph) body.set("ph", ph.get_distinct_id() || ""); } catch (e) {}
    fetch((ar ? "" : "/en") + "/search/ai/", { method: "POST", credentials: "same-origin", body: body })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.text(); })
      .then(function (html) {
        if (seq !== aiSeq) return;
        var tpl = document.createElement("template");
        tpl.innerHTML = html;
        var res = tpl.content.querySelector("[data-ai-results]");
        if (res) res.remove();
        var deep = tpl.content.querySelector(".ai-box__deep");
        if (deep) deep.remove(); // the deep answer button needs the full page script
        if (!tpl.content.querySelector(".ai-box__text, .ai-box__limit")) return;
        aiBox = el("section", "wrap");
        var card = el("div", "ai-box");
        var head = el("p", "ai-box__head");
        head.appendChild(el("span", null, ar ? "إجابة بالذكاء الاصطناعي" : "AI answer"));
        var content = el("div", "ai-box__body");
        content.appendChild(tpl.content);
        card.appendChild(head);
        card.appendChild(content);
        aiBox.appendChild(card);
        box.parentNode.insertBefore(aiBox, box);
        card.classList.add("ai-box--collapsed");
        if (content.scrollHeight <= content.clientHeight + 24) { card.classList.remove("ai-box--collapsed"); return; }
        var more = el("button", "ai-box__more", ar ? "عرض المزيد" : "Show more");
        more.type = "button";
        more.setAttribute("aria-expanded", "false");
        more.addEventListener("click", function () {
          var open = !card.classList.toggle("ai-box--collapsed");
          more.setAttribute("aria-expanded", String(open));
          more.textContent = open ? (ar ? "عرض أقل" : "Show less") : (ar ? "عرض المزيد" : "Show more");
        });
        card.appendChild(more);
      })
      .catch(function () {});
  }
  function run(q) {
    ai(q);
    if (!q) { box.textContent = ""; return; }
    box.textContent = T.busy;
    fetch(api + "/api/v1/search/?limit=20&q=" + encodeURIComponent(q), { headers: { Accept: "application/json" } })
      .then(function (r) { if (!r.ok) throw new Error(r.status); return r.json(); })
      .then(show)
      .catch(function () { box.textContent = ""; box.appendChild(el("p", "notice", T.fail)); });
  }
  var input = form.querySelector("input[name=q]");
  var q0 = new URLSearchParams(location.search).get("q") || "";
  if (input && q0) input.value = q0;
  run(q0.trim());
  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    var q = input.value.trim();
    history.replaceState(null, "", location.pathname + (q ? "?q=" + encodeURIComponent(q) : ""));
    run(q);
  });
})();
