"""Persist conservative token reservations before paid offline embedding attempts."""

import fcntl
import json
import os
import tempfile
from pathlib import Path


class BudgetError(ValueError):
    pass


class BudgetedEmbedder:
    """Count input tokens, reserve them durably, then request document vectors.

    Reservations include 16 tokens per document for task overhead. Failed or
    interrupted attempts remain reserved. This is a local spending guard, not
    an invoice; preserve the ledger when resuming a build.
    """

    def __init__(self, provider, max_tokens, ledger_path):
        if type(max_tokens) is not int or max_tokens <= 0:
            raise BudgetError("a positive input token budget is required")
        self.provider = provider
        self.model = provider.model
        self.max_tokens = max_tokens
        self.path = Path(ledger_path)
        self.path.parent.mkdir(parents=True, exist_ok=True)

    def _reserve(self, tokens, documents):
        flags = os.O_CREAT | os.O_RDWR | getattr(os, "O_NOFOLLOW", 0)
        fd = os.open(str(self.path) + ".lock", flags, 0o600)
        with os.fdopen(fd, "r+") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            identity = {
                "format_version": 1,
                "model": self.model,
                "max_tokens": self.max_tokens,
                "overhead_per_document": 16,
            }
            state = {**identity, "reserved_tokens": 0, "attempted_documents": 0, "attempted_batches": 0}
            if self.path.is_symlink():
                raise BudgetError("invalid embedding budget ledger")
            if self.path.exists():
                try:
                    state = json.loads(self.path.read_text())
                    if any(state.get(k) != v for k, v in identity.items()):
                        raise BudgetError("embedding budget identity mismatch")
                    if any(
                        type(state[k]) is not int or state[k] < 0
                        for k in ("reserved_tokens", "attempted_documents", "attempted_batches")
                    ):
                        raise ValueError()
                except (ValueError, KeyError, TypeError, AttributeError) as error:
                    if isinstance(error, BudgetError):
                        raise
                    raise BudgetError("invalid embedding budget ledger") from None
            if state["reserved_tokens"] + tokens > self.max_tokens:
                raise BudgetError("embedding input token budget exhausted")
            state["reserved_tokens"] += tokens
            state["attempted_documents"] += documents
            state["attempted_batches"] += 1
            temporary = None
            try:
                with tempfile.NamedTemporaryFile(mode="w", dir=self.path.parent, delete=False) as out:
                    temporary = Path(out.name)
                    json.dump(state, out, indent=2)
                    out.write("\n")
                    out.flush()
                    os.fsync(out.fileno())
                os.replace(temporary, self.path)
                directory = os.open(self.path.parent, os.O_RDONLY)
                try:
                    os.fsync(directory)
                finally:
                    os.close(directory)
            finally:
                if temporary is not None:
                    temporary.unlink(missing_ok=True)

    def embed(self, texts, query=False):
        if query or not texts:
            raise BudgetError("offline budget accepts document batches only")
        counted = self.provider.count_tokens(texts)
        if type(counted) is not int or counted <= 0:
            raise BudgetError("invalid input token count")
        self._reserve(counted + 16 * len(texts), len(texts))
        return self.provider.embed(texts, query=False)


class DailyBudgetedEmbedder:
    """Share conservative daily reservations across builds and worker restarts."""

    def __init__(self, provider, ledger_dir, daily_usd='1', usd_per_million='0.15',
                 timezone='Asia/Beirut', clock=None):
        from datetime import UTC, datetime
        from decimal import Decimal, InvalidOperation
        from zoneinfo import ZoneInfo

        try:
            cap, price = Decimal(str(daily_usd)), Decimal(str(usd_per_million))
            if not cap.is_finite() or not price.is_finite() or cap <= 0 or price <= 0:
                raise ValueError()
            self.max_tokens = int(cap * 1_000_000 / price)
            if self.max_tokens <= 0:
                raise ValueError()
            self.zone = ZoneInfo(timezone)
        except (ValueError, InvalidOperation, KeyError):
            raise BudgetError('invalid daily embedding budget') from None
        self.provider = provider
        self.model = provider.model
        self.root = Path(ledger_dir)
        self.clock = clock or (lambda: datetime.now(UTC))

    def count_tokens(self, texts):
        return self.provider.count_tokens(texts)

    def embed(self, texts, query=False):
        if query or not texts:
            raise BudgetError('offline budget accepts document batches only')
        counted = self.provider.count_tokens(texts)
        if type(counted) is not int or counted <= 0:
            raise BudgetError('invalid input token count')
        day = self.clock().astimezone(self.zone).date().isoformat()
        budget = BudgetedEmbedder(self.provider, self.max_tokens, self.root / (day + '.json'))
        budget._reserve(counted + 16 * len(texts), len(texts))
        return self.provider.embed(texts, query=False)
