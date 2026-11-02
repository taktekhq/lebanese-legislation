import importlib.util
import json
import os
from pathlib import Path
import tempfile
import tomllib
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('deploy', Path(__file__).parents[1] / 'scripts/origin-deploy.py')
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)


class DeploymentTests(unittest.TestCase):
    def test_stage_changes_only_idle_environment_and_does_not_write_flag(self):
        original = deploy.CONFIG.read_text()
        for live in ('blue', 'green', None):
            with self.subTest(live=live), tempfile.TemporaryDirectory() as tmp:
                config, state = Path(tmp) / 'wrangler.toml', Path(tmp) / 'state.json'
                config.write_text(original)
                with patch.object(deploy, 'CONFIG', config), patch.object(deploy, 'STATE', state), \
                     patch.object(deploy, 'marker', return_value=live) as marker, \
                     patch.dict(os.environ, {'GITHUB_OUTPUT': str(Path(tmp) / 'output')}):
                    result = deploy.stage('registry/new:2')
                    old = live or 'blue'
                    idle = 'green' if old == 'blue' else 'blue'
                    self.assertEqual(result, {'old': old, 'idle': idle, 'new_image': 'registry/new:2'})
                    self.assertEqual(json.loads(state.read_text()), result)
                    marker.assert_called_once_with('live-side')
                    before, after = tomllib.loads(original)['env'], tomllib.loads(config.read_text())['env']
                    self.assertEqual(before[old], after[old])
                    self.assertEqual(after[idle]['containers'][0]['image'], 'registry/new:2')

    def test_stage_deploys_and_sets_secrets_only_on_idle_side(self):
        for live in ('blue', 'green'):
            idle = 'green' if live == 'blue' else 'blue'
            with self.subTest(live=live), \
                 patch.object(deploy, 'wait_rollout'), \
                 patch('sys.argv', ['origin-deploy.py', 'stage']), \
                 patch.dict(os.environ, {'IMAGE': 'registry/new:2', 'ORIGIN_TEST_KEY': 'test',
                                        'GEMINI_API_KEY': 'gemini', 'LEBLEG_VECTORIZE_TOKEN': 'vector'}), \
                 patch.object(deploy, 'stage', return_value={'idle': idle}) as stage, \
                 patch.object(deploy.subprocess, 'run') as run:
                deploy.main()
                stage.assert_called_once_with('registry/new:2')
                self.assertEqual(len(run.call_args_list), 2)
                self.assertEqual(run.call_args_list[0].args[0][-3:], ['deploy', '--env', idle])
                self.assertEqual(run.call_args_list[1].args[0][-4:], ['secret', 'bulk', '--env', idle])
                self.assertTrue(all(c.kwargs['cwd'] == 'workers/origin' for c in run.call_args_list))
                self.assertEqual(json.loads(run.call_args_list[1].kwargs['input']),
                                 {'GEMINI_API_KEY': 'gemini', 'LEBLEG_VECTORIZE_TOKEN': 'vector'})

    def test_switch_and_rollback_only_write_flag_then_wait(self):
        state = {'old': 'blue', 'idle': 'green', 'new_image': 'registry/new:2'}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'state.json'
            path.write_text(json.dumps(state))
            for action, side in [('switch', 'green'), ('rollback', 'blue')]:
                events = []
                with self.subTest(action=action), patch.object(deploy, 'STATE', path), \
                     patch('sys.argv', ['origin-deploy.py', action]), \
                     patch.object(deploy.subprocess, 'run') as run, \
                     patch.object(deploy.time, 'sleep', side_effect=lambda n: events.append(('wait', n))), \
                     patch.object(deploy, 'marker', side_effect=lambda k, v: events.append((k, v))):
                    deploy.main()
                    self.assertEqual(events, [('live-side', side), ('wait', 20)])
                    run.assert_not_called()

    def test_invalid_live_side_or_image_fails_before_config_change(self):
        for live, image in [('red', 'registry/new:2'), ('blue', ''), ('green', 'bad\nimage')]:
            with patch.object(deploy, 'marker', return_value=live), patch.object(deploy, 'CONFIG') as config:
                with self.assertRaises(ValueError):
                    deploy.stage(image)
                config.write_text.assert_not_called()

    def test_site_worker_keeps_routes_mode_and_test_key(self):
        with patch.dict(os.environ, {'MODE': 'container', 'ROUTES': 'example.com/*, example.org/api/*',
                                    'ORIGIN_TEST_KEY': 'test'}), patch.object(deploy.subprocess, 'run') as run:
            deploy.deploy_site_worker()
            self.assertEqual(run.call_args_list[0].args[0][-6:],
                             ['--var', 'ORIGIN_MODE:container', '--route', 'example.com/*', '--route', 'example.org/api/*'])
            self.assertTrue(all(c.kwargs['cwd'] == 'workers/static-site' for c in run.call_args_list))

    def test_probe_preserves_named_user_agent(self):
        self.assertEqual(deploy._request('https://example.com').get_header('User-agent'), deploy.UA)


if __name__ == '__main__':
    unittest.main()
