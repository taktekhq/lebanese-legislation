/* Lebanese Legislation, static copy: search runs in the browser over every
   text's title, kind, number and year (search-index.json). The spelling
   folds match the server's ar_norm: tashkeel and tatweel dropped, أ إ آ ٱ → ا,
   ى → ي, ة → ه. A number with a year ("44/2015") finds that text directly. */
(function () {
  "use strict";
  var box = document.querySelector("[data-static-search]");
  if (!box) return;
  var base = box.getAttribute("data-base") || "/";
  var q = (new URLSearchParams(location.search).get("q") || "").trim();
  var input = document.querySelector('form.search input[name="q"]');
  if (input) input.value = q;
  if (!q) return;

  var title = document.querySelector("[data-search-title]");
  if (title) {
    title.textContent = "";
    var bdi = document.createElement("bdi");
    bdi.textContent = q;
    title.append("“", bdi, "”");
  }
  var hint = document.querySelector("[data-search-hint]");
  box.textContent = "Searching…";

  function norm(s) {
    return String(s).replace(/[ً-ٰٟـ]/g, "")
      .replace(/[أإآٱ]/g, "ا")
      .replace(/ى/g, "ي").replace(/ة/g, "ه").toLowerCase();
  }
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }

  fetch(box.getAttribute("data-index")).then(function (r) { return r.json(); }).then(function (idx) {
    var nq = norm(q), words = nq.split(/\s+/).filter(Boolean);
    var ny = q.match(/(\d+)\s*[\/\-]\s*(\d{4})/);
    var hits = [];
    // each law: [id, title, kind index, number, year, repealed, has text, code name]
    idx.laws.forEach(function (l) {
      var t = norm(l[1]), kind = idx.kinds[l[2]], score = 0;
      if (ny && l[3] === ny[1] && String(l[4]) === ny[2]) {
        score = 1000;
      } else {
        var hay = t + " " + norm(kind[0]) + " " + (l[3] || "") + " " + (l[4] || "");
        if (words.every(function (w) { return hay.indexOf(w) >= 0; })) {
          score = 10 + (l[7] ? 100 : 0) + (t.indexOf(nq) >= 0 ? 30 : 0) + (t.indexOf(nq) === 0 ? 20 : 0) + (l[6] ? 5 : 0);
        }
      }
      if (score) hits.push([score, l]);
    });
    hits.sort(function (a, b) { return b[0] - a[0] || (b[1][4] || 0) - (a[1][4] || 0) || b[1][0] - a[1][0]; });
    box.textContent = "";
    if (hint) hint.hidden = true;
    if (!hits.length) {
      var empty = el("div", "empty");
      empty.append(el("h3", null, "No title matches."), el("p", null, "Try fewer words, the spelling used in the Official Gazette, or a number and year such as 44/2015."));
      box.append(empty);
      return;
    }
    var head = el("div", "sec__head");
    head.style.marginTop = "40px";
    var h2 = el("h2", "sec__title", "Texts ");
    h2.style.fontSize = "1.375rem";
    h2.append(el("span", "muted", hits.length.toLocaleString("en")));
    var wrap = el("div");
    wrap.append(h2);
    head.append(wrap);
    var ul = el("ul", "doclist");
    hits.slice(0, 100).forEach(function (h) {
      var l = h[1], kind = idx.kinds[l[2]];
      var a = el("a", "docrow");
      a.href = base + "law/" + l[0] + "/";
      a.dir = "rtl";
      var t = el("span", "docrow__title", l[1]);
      t.lang = "ar";
      var k = el("span", "kicker seps");
      var b = el("b", null, kind[0]);
      b.lang = "ar";
      k.append(b);
      [kind[1], l[7], l[3] && "No. " + l[3], l[4] || null].forEach(function (x) {
        if (!x) return;
        var s = el("span"), i = el("bdi", null, String(x));
        s.append(i);
        k.append(s);
      });
      var st = el("span", l[5] ? "status status--repealed" : "status", l[5] ? "Repealed" : "In force");
      a.append(t, k, st);
      var li = el("li");
      li.append(a);
      ul.append(li);
    });
    box.append(head, ul);
    if (hits.length > 100) box.append(el("p", "tiny", "Showing the first 100. Add a word or the year to narrow it down."));
  }).catch(function () {
    box.textContent = "Search could not load. Try again in a moment.";
  });
})();
