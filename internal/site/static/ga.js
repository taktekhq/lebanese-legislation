/* Google Analytics 4: property lebaneselegislation.com (G-RDYRK422NP), the
   standard gtag.js install. A file, not an inline script, so the Content
   Security Policy needs no extra hash. assets/load.js fetches gtag.js and
   PostHog (ph.js) after the page; what is queued here is sent then.

   Loaded unless the server runs with ANALYTICS=off. No consent
   banner: when on, analytics are always on. GA4 gets each
   page's real title and full address (utm_source=chatgpt.com is how visits
   from the ChatGPT app are recognised) and works out the traffic source
   itself, so no event here may carry a source, medium or campaign parameter:
   GA4 reads those as the visit's source.

   Events, besides page views. Each goes to GA4 and to PostHog under the same
   name and properties (track below), so the two can be compared:
     search              a search was run (search_term, results)
     search_no_results   ... and found nothing (search_term)
     document_open       a law, decree, ruling or other document was opened
     document_download   a PDF or dataset link was followed (format)
     contribution_click  a link to the GitHub repository was followed
     cite_copy           a citation was copied
     language_switch     the link to the other language was followed
     blog_read           a journal post was opened (blog_slug)
     navigation_click    a header or footer link was followed (surface) */
window.dataLayer = window.dataLayer || [];
function gtag() { dataLayer.push(arguments); }
gtag("js", new Date());
gtag("config", "G-RDYRK422NP");

function track(name, params) {
  params = params || {};
  params.language = document.documentElement.lang === "ar" ? "ar" : "en";
  gtag("event", name, params);
  if (window.posthog && window.posthog.capture) window.posthog.capture(name, params);
}

document.addEventListener("DOMContentLoaded", function () {
  var path = location.pathname.replace(/^\/en\//, "/");
  if (/^\/(law|decree|text|ruling|gazette|opinion|agreement|doctrine|research)\/\d/.test(path)) track("document_open");
  var blog = document.querySelector("[data-blog]");
  if (blog) track("blog_read", { blog_slug: blog.getAttribute("data-blog") });
  var meta = document.getElementById("search-meta");
  if (meta) {
    var n = parseInt(meta.getAttribute("data-n"), 10) || 0;
    var q = meta.getAttribute("data-q");
    track("search", { search_term: q, results: n });
    if (n === 0) track("search_no_results", { search_term: q });
  }
});

document.addEventListener("click", function (ev) {
  var el = ev.target.closest && ev.target.closest("a,[data-ga],[data-cite]");
  if (!el) return;
  if (el.hasAttribute("data-cite")) return track("cite_copy");
  var tag = el.getAttribute("data-ga") || "";
  var href = el.getAttribute("href") || "";
  if (/download/.test(tag) || el.hasAttribute("download") || /\.(pdf|json|parquet|csv)(\?|$)/.test(href)) {
    return track("document_download", { format: /\.pdf(\?|$)/.test(href) ? "pdf" : "dataset" });
  }
  if (tag) return track(tag);
  if (el.classList.contains("lang-switch")) return track("language_switch");
  if (/^https:\/\/github\.com\/taktekhq\//.test(href)) return track("contribution_click");
  var area = el.closest("nav,footer");
  if (area) track("navigation_click", { surface: area.tagName === "FOOTER" ? "footer" : "header" });
});
