import pytest

from lebleg_starter.pipeline_metadata import update_block


def test_managed_card_preserves_yaml_and_attribution_and_is_idempotent():
    original='---\nlicense: other\n---\n\n# Dataset\n\nCredit the source.\n'
    updated=update_block(original,'[Download](https://example.org/dump)')
    assert updated.startswith('---\nlicense: other\n---')
    assert 'Credit the source.' in updated
    assert updated.count('Download')==1
    assert update_block(updated,'[Download](https://example.org/dump)')==updated
    assert 'New' in update_block(updated,'New') and 'Download' not in update_block(updated,'New')


def test_broken_managed_block_refuses_to_overwrite():
    with pytest.raises(ValueError):update_block('# Dataset\n<!-- lebleg-release:start -->\nbroken','new')
