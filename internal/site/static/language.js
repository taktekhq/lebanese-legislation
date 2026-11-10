/* An explicit language choice belongs to this browser. Keep it client-side:
   the same public HTML can still be cached and indexed in both languages. */
(function () {
  "use strict";
  var key = "lebleg-language";
  var current = document.documentElement.lang;

  /* The pages that have a French version (frenchPaths in lang.go). */
  var french = ["/", "/laws/", "/codes/", "/topics/", "/rulings/", "/gazette/", "/changes/", "/opinions/", "/agreements/", "/doctrine/", "/research/", "/laws/years/", "/rulings/years/", "/gazette/years/", "/search/", "/data/", "/ai/", "/run-it-yourself/", "/about/", "/sources/", "/legiliban/", "/contribute/", "/privacy/", "/terms/"];

  /* This page in another language, or "" when it has no French version. */
  function address(language) {
    var url = new URL(location.href);
    var path = url.pathname.replace(/^\/(?:en|fr)(?:\/|$)/, "/");
    if (language === "fr" && french.indexOf(path) < 0) return "";
    url.pathname = language === "ar" ? path : "/" + language + path;
    return url.href;
  }

  function restoreLanguage() {
    var saved;
    try { saved = localStorage.getItem(key); } catch (_) {}
    if ((saved === "en" || saved === "ar" || saved === "fr") && saved !== current) {
      var to = address(saved);
      if (to) location.replace(to);
    }
  }
  restoreLanguage();
  window.addEventListener("pageshow", function (event) {
    if (event.persisted) restoreLanguage();
  });

  function chooseLanguage(event) {
    if (event.type === "auxclick" && event.button !== 1) return;
    var link = event.target.closest("a.lang-switch");
    if (!link || event.defaultPrevented) return;
    var language = link.getAttribute("hreflang");
    if (language !== "en" && language !== "ar" && language !== "fr") return;
    try { localStorage.setItem(key, language); } catch (_) {}
    // Leave native navigation intact, including keyboard and modifier clicks.
    // A page with no French version keeps the link's own address, the French home page.
    var to = address(language);
    if (to) link.href = to;
  }
  document.addEventListener("click", chooseLanguage);
  document.addEventListener("auxclick", chooseLanguage);
})();
