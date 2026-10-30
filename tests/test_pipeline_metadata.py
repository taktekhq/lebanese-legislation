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


def test_retrying_old_release_cannot_overwrite_newer_card_or_overlay(tmp_path,monkeypatch):
    import json

    import huggingface_hub
    from test_pipeline import Hub, config
    from test_pipeline_publish import Hub as WriteHub
    from test_runtime import manifest

    from lebleg_starter.pipeline import Coordinator
    from lebleg_starter.pipeline_steps import ReleaseSteps
    settings=config(tmp_path);overlay=tmp_path/'overlay.md';settings['sources'][0]['card_block_file']=str(overlay)
    co=Coordinator(settings,hub=Hub());job_id=co.ledger.observe('org/data','a'*40,'1'*64,approved=True);job=co.ledger.job(job_id)
    directory=co.root/'jobs'/job['release_id']/'release';directory.mkdir(parents=True)
    m=manifest();m['release_id']=job['release_id'];m['source']={'repo_id':job['repo'],'revision':job['revision']}
    (directory/'manifest.json').write_text(json.dumps(m));overlay.write_text('newer release')
    hub=WriteHub();hub.files['README.md']=b'# Dataset\nNewer release';hub.files['releases/latest.json']=b'{"release_id":"newer-release"}'
    monkeypatch.setattr(huggingface_hub,'HfApi',lambda token=None:hub)
    steps=ReleaseSteps(co);monkeypatch.setattr(steps,'cleanup',lambda job:None)
    assert steps.metadata(job)['status']=='skipped'
    assert hub.files['README.md']==b'# Dataset\nNewer release'
    assert overlay.read_text()=='newer release'
    assert hub.commits==0
