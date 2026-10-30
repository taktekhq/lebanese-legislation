import unittest
from mcp_release import select_release


class ReleasePolicy(unittest.TestCase):
    def test_fresh_version_builds(self):
        p=select_release('0.1.5','a'*40,'sha256:'+'b'*64,[])
        self.assertTrue(p['build']);self.assertFalse(p['skip'])
        self.assertIn('0.1.5',p['candidate'])

    def test_existing_immutable_version_is_reused_not_rebuilt(self):
        p=select_release('0.1.5','a'*40,'sha256:'+'b'*64,[['0.1.5','source-old','latest']])
        self.assertFalse(p['build']);self.assertEqual(p['candidate'],'0.1.5')

    def test_older_upstream_cannot_rewind_latest(self):
        p=select_release('0.1.4','a'*40,'sha256:'+'b'*64,[['0.1.5','latest']])
        self.assertTrue(p['skip'])

    def test_partial_retry_has_same_unique_candidate(self):
        first=select_release('0.1.5','a'*40,'sha256:'+'b'*64,[])
        self.assertEqual(first,select_release('0.1.5','a'*40,'sha256:'+'b'*64,[]))
        self.assertNotEqual(first['candidate'],select_release('0.1.5','a'*40,'sha256:'+'c'*64,[])['candidate'])

    def test_invalid_values_are_refused(self):
        with self.assertRaises(ValueError):select_release('latest','a'*40,'sha256:'+'b'*64,[])
        with self.assertRaises(ValueError):select_release('0.1.5','bad','sha256:'+'b'*64,[])


if __name__=='__main__':unittest.main()
