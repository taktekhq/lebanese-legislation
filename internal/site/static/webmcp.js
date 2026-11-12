/* The site's search and article lookup as tools for an AI assistant that runs
   in the visitor's browser (WebMCP, document.modelContext). They call the
   same JSON API as everyone else (/api/v1/) and return its answer unchanged:
   the texts word for word. In a browser without WebMCP this does nothing. */
(function () {
  "use strict";
  var mc = document.modelContext || navigator.modelContext;
  if (!mc || typeof mc.registerTool !== "function") return;

  function text(o) {
    return { content: [{ type: "text", text: typeof o === "string" ? o : JSON.stringify(o) }] };
  }
  function get(path) {
    return fetch(path, { headers: { Accept: "application/json" } }).then(function (r) {
      if (r.status === 404) return text("Nothing at " + path + ". Check the id with search_legislation.");
      if (!r.ok) return text("The site answered " + r.status + ". Wait a few seconds and try again.");
      return r.json().then(text);
    });
  }
  function register(tool) {
    try {
      var p = mc.registerTool(tool);
      if (p && p.catch) p.catch(function () {});
    } catch (e) {}
  }

  register({
    name: "search_legislation",
    description: "Search Lebanese laws, decrees, articles, court rulings and Official Gazette issues on LebaneseLegislation.com. " +
      "Takes a question, a few words or a citation (\"المادة 50 من قانون العمل\", \"article 50 labour code\") in Arabic, English or French. " +
      "Returns ids, titles, dates, status and links; texts are quoted word for word. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        query: { type: "string", description: "What to look for, under 200 characters" },
        type: { type: "string", enum: ["laws", "articles", "rulings", "gazette"], description: "Only one kind of result; all four when left out" },
        year: { type: "integer", description: "Only texts of this year" },
        limit: { type: "integer", description: "Results of each kind, 10 by default, at most 50" }
      },
      required: ["query"]
    },
    annotations: { readOnlyHint: true },
    execute: function (a) {
      var q = new URLSearchParams({ q: String((a && a.query) || "") });
      if (a && a.type) q.set("type", String(a.type));
      if (a && a.year) q.set("year", String(a.year));
      if (a && a.limit) q.set("limit", String(a.limit));
      return get("/api/v1/search/?" + q.toString());
    }
  });

  register({
    name: "get_article",
    description: "The exact wording of one article of a Lebanese law or decree, with its status and the texts that amended it. " +
      "Give the text's id (from search_legislation) and the article number. Cite it by the url in the answer. Not legal advice.",
    inputSchema: {
      type: "object",
      properties: {
        law_id: { type: "integer", description: "Id of the law or decree, e.g. 190374 for the Labour Code" },
        article: { type: "string", description: "Article number as the text writes it, e.g. \"50\" or \"12-1\"" }
      },
      required: ["law_id", "article"]
    },
    annotations: { readOnlyHint: true },
    execute: function (a) {
      var id = parseInt(a && a.law_id, 10);
      var n = String((a && a.article) || "").trim();
      if (!(id > 0) || !n) return Promise.resolve(text("Give law_id (a number) and article."));
      return get("/api/v1/law/" + id + "/article/" + encodeURIComponent(n) + "/");
    }
  });
})();
