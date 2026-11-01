/* An explicit language choice belongs to this browser. Keep it client-side:
   the same public HTML can still be cached and indexed in both languages. */
(function () {
  "use strict";
  var key = "lebleg-language";
  var current = document.documentElement.lang;

  function address(language) {
    var url = new URL(location.href);
    var path = url.pathname.replace(/^\/en(?:\/|$)/, "/");
    url.pathname = language === "en" ? "/en" + path : path;
    return url.href;
  }

  function restoreLanguage() {
    var saved;
    try { saved = localStorage.getItem(key); } catch (_) {}
    if ((saved === "en" || saved === "ar") && saved !== current) {
      location.replace(address(saved));
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
    if (language !== "en" && language !== "ar") return;
    try { localStorage.setItem(key, language); } catch (_) {}
    // Leave native navigation intact, including keyboard and modifier clicks.
    link.href = address(language);
  }
  document.addEventListener("click", chooseLanguage);
  document.addEventListener("auxclick", chooseLanguage);
})();
