import json
from concurrent.futures import ThreadPoolExecutor

import pytest

from lebleg.budget import BudgetedEmbedder, BudgetError


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


def test_daily_cap_is_shared_by_jobs_and_resets_in_beirut(tmp_path):
    from datetime import UTC, datetime

    from lebleg.budget import DailyBudgetedEmbedder

    provider = Provider()
    now = [datetime(2026, 10, 30, 21, 59, tzinfo=UTC)]
    # 26 reserved tokens cost exactly the deliberately tiny test cap.
    kwargs = {'daily_usd':'0.0000039', 'usd_per_million':'0.15', 'clock':lambda: now[0]}
    DailyBudgetedEmbedder(provider, tmp_path, **kwargs).embed(['one'])
    with pytest.raises(BudgetError, match='exhausted'):
        DailyBudgetedEmbedder(provider, tmp_path, **kwargs).embed(['two'])
    now[0] = datetime(2026, 10, 30, 22, 1, tzinfo=UTC)
    DailyBudgetedEmbedder(provider, tmp_path, **kwargs).embed(['next day'])
    assert provider.calls == 2
    assert len(list(tmp_path.glob('*.json'))) == 2


def test_daily_budget_refuses_invalid_prices(tmp_path):
    from lebleg.budget import DailyBudgetedEmbedder

    for price in ['0', '-1', 'NaN', 'Infinity']:
        with pytest.raises(BudgetError):
            DailyBudgetedEmbedder(Provider(), tmp_path, usd_per_million=price)


def test_daily_reservation_uses_day_after_token_count(tmp_path):
    from datetime import UTC, datetime

    from lebleg.budget import DailyBudgetedEmbedder

    now=[datetime(2026,10,30,21,59,tzinfo=UTC)]
    class SlowCount(Provider):
        def count_tokens(self,texts):
            now[0]=datetime(2026,10,30,22,1,tzinfo=UTC)
            return super().count_tokens(texts)
    DailyBudgetedEmbedder(SlowCount(),tmp_path,clock=lambda:now[0]).embed(['one'])
    assert (tmp_path/'2026-10-31.json').exists()
    assert not (tmp_path/'2026-10-30.json').exists()
