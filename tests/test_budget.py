import json
from concurrent.futures import ThreadPoolExecutor

import pytest

from lebleg_starter.budget import BudgetedEmbedder, BudgetError


class Provider:
    model = "gemini-embedding-001"

    def __init__(self):
        self.calls = 0

    def count_tokens(self, texts):
        return 10 * len(texts)

    def embed(self, texts, query=False):
        self.calls += 1
        return [[0.1] * 768 for _ in texts]


def test_reservation_precedes_paid_call_and_survives_restart(tmp_path):
    path = tmp_path / "budget.json"
    provider = Provider()
    BudgetedEmbedder(provider, 52, path).embed(["one"])
    BudgetedEmbedder(provider, 52, path).embed(["two"])
    with pytest.raises(BudgetError, match="exhausted"):
        BudgetedEmbedder(provider, 52, path).embed(["three"])
    assert provider.calls == 2
    assert json.loads(path.read_text())["reserved_tokens"] == 52
    assert path.stat().st_mode & 0o777 == 0o600


def test_concurrent_instances_cannot_overreserve(tmp_path):
    provider = Provider()
    wrappers = [BudgetedEmbedder(provider, 26, tmp_path / "budget.json") for _ in range(8)]

    def attempt(wrapper):
        try:
            wrapper.embed(["one"])
            return True
        except BudgetError:
            return False

    with ThreadPoolExecutor(max_workers=8) as pool:
        assert sum(pool.map(attempt, wrappers)) == 1
    assert provider.calls == 1


def test_failed_paid_attempt_is_still_reserved(tmp_path):
    class Broken(Provider):
        def embed(self, texts, query=False):
            raise ValueError("provider failed")

    path = tmp_path / "budget.json"
    with pytest.raises(ValueError, match="provider failed"):
        BudgetedEmbedder(Broken(), 26, path).embed(["one"])
    with pytest.raises(BudgetError, match="exhausted"):
        BudgetedEmbedder(Provider(), 26, path).embed(["one"])


def test_invalid_ledger_or_changed_cap_refused(tmp_path):
    path = tmp_path / "budget.json"
    path.write_text("{invalid")
    with pytest.raises(BudgetError):
        BudgetedEmbedder(Provider(), 26, path).embed(["one"])
    path.unlink()
    BudgetedEmbedder(Provider(), 26, path).embed(["one"])
    with pytest.raises(BudgetError, match="identity"):
        BudgetedEmbedder(Provider(), 52, path).embed(["one"])
