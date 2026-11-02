import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('deploy', Path(__file__).parents[1] / 'scripts/origin-deploy.py')
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


class DeploymentTests(unittest.TestCase):
    def test_stage_pins_live_image_and_switch_and_rollback_keep_both_images(self):
        original = deploy.CONFIG.read_text()
        for live in ('blue', 'green'):
            with self.subTest(live=live), tempfile.TemporaryDirectory() as tmp:
                config, state = Path(tmp) / 'wrangler.toml', Path(tmp) / 'state.json'
                config.write_text(original)
                markers = {'live-side': live, 'live-image': 'registry/old:1'}
                def marker(name, value=None):
                    if value is not None:
                        markers[name] = value
                    return markers.get(name)
                with patch.object(deploy, 'CONFIG', config), patch.object(deploy, 'STATE', state), \
                     patch.object(deploy, 'api', return_value={'result': {'bindings': [{'name': 'LIVE', 'text': live}]}}), \
                     patch.object(deploy, 'marker', side_effect=marker), \
                     patch.dict(os.environ, {'GITHUB_OUTPUT': str(Path(tmp) / 'output')}):
                    deploy.stage('registry/new:2')
                    data = json.loads(state.read_text())
                    self.assertEqual(data['images'][live], 'registry/old:1')
                    self.assertEqual(data['images'][data['idle']], 'registry/new:2')
                    for side in (data['idle'], live):
                        deploy.configure(data, side)
                        import tomllib
                        parsed = tomllib.loads(config.read_text())
                        self.assertEqual(parsed['vars']['LIVE'], side)
                        self.assertEqual({c['class_name']: c['image'] for c in parsed['containers']},
                                         {'Origin' + s.title(): i for s, i in data['images'].items()})

    def test_bootstrap_uses_current_origin_application(self):
        with tempfile.TemporaryDirectory() as tmp:
            config = Path(tmp) / 'wrangler.toml'
            config.write_text(deploy.CONFIG.read_text())
            responses = [{'result': {'bindings': [{'name': 'ORIGIN', 'namespace_id': 'old-ns'}]}},
                         {'result': [{'durable_objects': {'namespace_id': 'old-ns'},
                                      'configuration': {'image': 'registry/old:1'}}]}]
            with patch.object(deploy, 'CONFIG', config), patch.object(deploy, 'STATE', Path(tmp) / 'state.json'), \
                 patch.object(deploy, 'api', side_effect=responses), patch.object(deploy, 'marker', return_value=None), \
                 patch.dict(os.environ, {'GITHUB_OUTPUT': str(Path(tmp) / 'output')}):
                deploy.stage('registry/new:2')
                state = json.loads(deploy.STATE.read_text())
                self.assertEqual(state['old'], 'blue')
                self.assertEqual(state['old_image'], 'registry/old:1')

    def test_switch_and_rollback_deploy_before_updating_markers(self):
        state = {'old': 'blue', 'idle': 'green',
                 'images': {'blue': 'registry/old:1', 'green': 'registry/new:2'}}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'state.json'
            path.write_text(json.dumps(state))
            for action, side in [('switch', 'green'), ('rollback', 'blue')]:
                events = []
                with self.subTest(action=action), patch.object(deploy, 'STATE', path), \
                     patch('sys.argv', ['origin-deploy.py', action]), \
                     patch.object(deploy, 'configure', side_effect=lambda data, live: events.append(('configure', live))), \
                     patch.object(deploy, 'deploy', side_effect=lambda: events.append(('deploy',))), \
                     patch.object(deploy, 'marker', side_effect=lambda key, value: events.append((key, value))):
                    deploy.main()
                    self.assertEqual(events, [('configure', side), ('deploy',),
                                              ('live-side', side), ('live-image', state['images'][side])])


if __name__ == '__main__':
    unittest.main()
