import importlib.util
import io
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

SHA = '0123456789abcdef0123456789abcdef01234567'
OLD_SHA = 'abcdef0123456789abcdef0123456789abcdef01'


class DeploymentTests(unittest.TestCase):
    def test_stage_changes_only_idle_environment_and_does_not_write_flag(self):
        original = deploy.CONFIG.read_text()
        for live in ('blue', 'green', None):
            with self.subTest(live=live), tempfile.TemporaryDirectory() as tmp:
                config, state = Path(tmp) / 'wrangler.toml', Path(tmp) / 'state.json'
                config.write_text(original)
                with patch.object(deploy, 'CONFIG', config), patch.object(deploy, 'STATE', state), \
                     patch.object(deploy, 'marker', return_value=live) as marker, \
                     patch.dict(os.environ, {'GITHUB_OUTPUT': str(Path(tmp) / 'output'), 'EXPECTED_BUILD': SHA}):
                    result = deploy.stage('registry/new:2')
                    old = live or 'blue'
                    idle = 'green' if old == 'blue' else 'blue'
                    self.assertEqual(result, {'old': old, 'idle': idle, 'new_image': 'registry/new:2', 'expected_build': SHA})
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

    def test_site_worker_keeps_routes_and_test_key(self):
        with patch.dict(os.environ, {'ROUTES': 'example.com/*, example.org/api/*',
                                    'ORIGIN_TEST_KEY': 'test'}), patch.object(deploy.subprocess, 'run') as run:
            deploy.deploy_site_worker()
            self.assertEqual(run.call_args_list[0].args[0],
                             ['npx', '--yes', 'wrangler@4.151.1', 'deploy', '--route', 'example.com/*', '--route', 'example.org/api/*'])
            self.assertTrue(all(c.kwargs['cwd'] == 'workers/static-site' for c in run.call_args_list))

    def test_image_build_uses_revision_or_commit_tag_and_rejects_unknown(self):
        self.assertEqual(deploy.image_build('registry/image:latest', SHA), SHA)
        self.assertEqual(deploy.image_build('registry/image:' + OLD_SHA, SHA), SHA)
        self.assertEqual(deploy.image_build('registry/image:source-' + SHA), SHA)
        self.assertEqual(deploy.image_build('registry/image:' + SHA + '-123-1'), SHA)
        for image, revision in [('registry/image:latest', ''), ('registry/image:1234567', ''),
                                ('registry/image:latest', 'unknown')]:
            with self.subTest(image=image, revision=revision), self.assertRaises(ValueError):
                deploy.image_build(image, revision)

    def probe(self, responses, seconds=10, side=None):
        now = [0]
        self.health_requests = []

        def sleep(seconds):
            now[0] += seconds

        def urlopen(req, timeout):
            response = io.BytesIO(b'')
            response.status = 200
            if req.full_url.endswith('/healthz'):
                self.health_requests.append(req)
                status, body = responses.pop(0) if len(responses) > 1 else responses[0]
                response.status = status
                response.write(body)
                response.seek(0)
            return response

        with patch.object(deploy.time, 'monotonic', side_effect=lambda: now[0]), \
             patch.object(deploy.time, 'sleep', side_effect=sleep), \
             patch.object(deploy.urllib.request, 'urlopen', side_effect=urlopen), \
             patch.dict(os.environ, {'ORIGIN_TEST_KEY': 'secret'}):
            deploy.check(seconds, SHA, side)

    def test_check_retries_old_build_until_expected_on_idle_and_public(self):
        for side in ('green', None):
            with self.subTest(side=side):
                self.probe([(200, json.dumps({'build': OLD_SHA}).encode()),
                            (200, json.dumps({'build': SHA}).encode())], side=side)
                self.assertEqual(len(self.health_requests), 2)
                for req in self.health_requests:
                    self.assertEqual(req.get_header('X-origin-side'), side)
                    self.assertEqual(req.get_header('X-origin-test'), 'secret' if side else None)

    def test_check_rejects_bad_status_missing_and_malformed_builds(self):
        for status, body in [(503, json.dumps({'build': SHA}).encode()),
                             (200, b'{}'), (200, b'[]'), (200, b'not json'),
                             (200, b'{"build":"unknown"}')]:
            with self.subTest(status=status, body=body):
                with self.assertRaises(SystemExit) as err:
                    self.probe([(status, body)], seconds=6)
                self.assertIn(SHA, str(err.exception))
                self.assertIn('build seen', str(err.exception))

    def test_check_timeout_reports_seen_and_expected_build(self):
        with self.assertRaises(SystemExit) as err:
            self.probe([(200, json.dumps({'build': OLD_SHA}).encode())], seconds=6)
        self.assertIn(OLD_SHA, str(err.exception))
        self.assertIn(SHA, str(err.exception))
        self.assertEqual(len(self.health_requests), 2)

    def test_warm_and_three_public_checks_use_staged_build(self):
        state = {'idle': 'green', 'expected_build': SHA}
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'state.json'
            path.write_text(json.dumps(state))
            for action in ('warm', 'verify'):
                with self.subTest(action=action), patch.object(deploy, 'STATE', path), \
                     patch('sys.argv', ['origin-deploy.py', action]), \
                     patch.object(deploy, 'check') as check, \
                     patch.object(deploy.time, 'sleep') as sleep:
                    deploy.main()
                    if action == 'warm':
                        check.assert_called_once_with(1800, SHA, 'green')
                        sleep.assert_not_called()
                    else:
                        self.assertEqual([c.args for c in check.call_args_list], [(60, SHA)] * 3)
                        self.assertEqual([c.args for c in sleep.call_args_list], [(120,), (120,)])

    def test_probe_preserves_named_user_agent(self):
        self.assertEqual(deploy._request('https://example.com').get_header('User-agent'), deploy.UA)


if __name__ == '__main__':
    unittest.main()
