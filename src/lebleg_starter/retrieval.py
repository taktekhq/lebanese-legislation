"""One retrieval facade for HTTP resources and MCP tools."""

from urllib.parse import quote

from .general import GeneralNotFound


class RetrievalAPI:
    def __init__(self, search_service, general_service):
        self.service = search_service
        self.general = general_service

    def passages(self, query, limit=8, semantic=True, select=False, **filters):
        return self.service.search(query, limit, semantic, select, **filters)

    def legacy(self, query, kind=None, year=None, limit=10, offset=0, semantic=False, select=False):
        """Preserve v1 categories while obtaining every passage from the same engine."""
        query = query.strip()
        result = {"q": query, "law_count": 0, "article_count": 0, "ruling_count": 0, "gazette_count": 0}
        if not query or len(query) > 500:
            return result
        groups = {"laws": ["law"], "articles": ["article"], "rulings": ["ruling"]}
        kinds = groups.get(kind, ["law", "article", "ruling"])
        if kind != "gazette":
            core = self.passages(
                query, limit, semantic, select, kinds=kinds, year=year or None, offset=offset, include_counts=True
            )
            result.update({k: v for k, v in core.items() if k != "exact_counts"})
            counts = core.get("exact_counts", {})
            for source_kind, field in [("law", "law_count"), ("article", "article_count"), ("ruling", "ruling_count")]:
                result[field] = counts.get(source_kind, 0)
            seen = set()
            for passage in core["passages"]:
                source_kind, source_id, part = passage["kind"], passage["source_id"], passage["part"]
                identity = (source_kind, source_id, part if source_kind == "article" else "")
                if identity in seen:
                    continue
                seen.add(identity)
                try:
                    if source_kind == "law":
                        result.setdefault("laws", []).append(self.general.summary(source_id))
                    elif source_kind == "article":
                        law = self.general.summary(source_id)
                        result.setdefault("articles", []).append(
                            {
                                "law": law,
                                "article_number": part,
                                "snippet": passage["text"],
                                "url": f"{law['url']}article/{quote('-'.join(part.split()), safe='')}/",
                            }
                        )
                    elif source_kind == "ruling":
                        ruling = self.general.ruling(source_id)
                        hit = {
                            k: ruling[k]
                            for k in ("id", "title", "court", "decided_on", "url", "pdf_url")
                            if ruling.get(k)
                        }
                        hit["snippet"] = passage["text"]
                        if part.isdigit():
                            hit["judgment_page"] = int(part)
                        result.setdefault("rulings", []).append(hit)
                except GeneralNotFound:
                    # A stale source reference never becomes a made-up resource.
                    continue
            if semantic:
                result["count_mode"] = "returned_items"
                for field, collection in [
                    ("law_count", "laws"),
                    ("article_count", "articles"),
                    ("ruling_count", "rulings"),
                ]:
                    result[field] = len(result.get(collection, []))
            else:
                result["count_mode"] = "exact_matches"
        if kind not in groups:
            result.update(self.general.gazette_search(query, limit=limit, offset=offset))
        return result
