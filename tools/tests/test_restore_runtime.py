import pytest

from lebleg.restore import RestoreError, target_state


class Result:
    def __init__(self, row):
        self.row = row

    def fetchone(self):
        return self.row

    def fetchall(self):
        return self.row


class Connection:
    def __init__(self, tables, receipt=None):
        self.tables = tables
        self.receipt = receipt

    def execute(self, query, params=None):
        if "pg_tables" in query:
            return Result([(x,) for x in self.tables])
        return Result(self.receipt)


def test_restore_refuses_occupied_database():
    with pytest.raises(RestoreError):
        target_state(Connection(["unrelated"]), {"release_id": "r"})


def test_restore_rerun_requires_matching_manifest():
    manifest = {"release_id": "r"}
    assert target_state(Connection(["restore_receipt"], (manifest,)), manifest) == "ready"
    with pytest.raises(RestoreError):
        target_state(Connection(["restore_receipt"], ({"release_id": "other"},)), manifest)


def test_empty_database_can_restore():
    assert target_state(Connection([]), {"release_id": "r"}) == "empty"


def test_same_release_repin_keeps_idempotence():
    checked = {"release_id": "r", "artifact_revision": "a" * 40}
    rerun = {"release_id": "r", "artifact_revision": "b" * 40}
    assert target_state(Connection(["restore_receipt"], (checked,)), rerun) == "ready"
