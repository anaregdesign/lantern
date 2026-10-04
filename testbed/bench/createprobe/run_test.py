import copy
import importlib.util
import io
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('create_runner', Path(__file__).with_name('run.py'))
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)

class ContractTest(unittest.TestCase):
    def test_native_shutdown_is_explicit_and_idempotent(self):
        class Input(io.BytesIO):
            def close(self):
                self.sent = self.getvalue()
                super().close()
        class Process:
            stdin = Input()
            def wait(self, timeout):
                if not self.stdin.closed or self.stdin.sent != b'shutdown\n':
                    raise AssertionError('EOF is not the native shutdown command')
                self.timeout = timeout
                return 0
            def terminate(self):
                raise AssertionError('successful explicit shutdown must not signal')
        child = Process()
        self.assertTrue(runner.stop(child))
        self.assertTrue(runner.stop(child))
        self.assertEqual(child.timeout, 30)
    def test_bounded_seconds(self):
        self.assertEqual(runner.seconds('60s'), 60)
        for bad in ['0s', '-1s', '301s', 'NaNs', 'infs', '1m', None]:
            with self.assertRaises(ValueError):
                runner.seconds(bad)
    def test_no_ha_or_silent_workload_drift(self):
        config = {'name':'edge_create','cluster':{'topology':'standalone','auth_mode':'oidc','receipt_wal':{'enabled':True,'max_entries':32768,'max_bytes':16777216}}, 'phases':{'warmup':{'duration':'10s','rps':100,'concurrency':1},'steady':{'duration':'60s','rps':100,'concurrency':1},'cooldown':'5s'},'target':{'driver':'standalone_create','families':['plain','receipt']}}
        self.assertEqual(runner.validate(config), config)
        for path, bad in [(('cluster','topology'),'ha'),(('cluster','auth_mode'),'off'),(('target','families'),['plain']), (('phases','steady','rps'),50), (('cluster','receipt_wal','max_entries'),512)]:
            mutated = copy.deepcopy(config); part = mutated
            for key in path[:-1]: part = part[key]
            part[path[-1]] = bad
            with self.assertRaises(ValueError): runner.validate(mutated)

if __name__ == '__main__': unittest.main()
