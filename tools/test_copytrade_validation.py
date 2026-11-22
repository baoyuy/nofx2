import importlib.util
import unittest
from pathlib import Path


spec = importlib.util.spec_from_file_location(
    'copytrade_validation', Path(__file__).with_name('copytrade_validation.py')
)
validation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validation)


class ValidationTest(unittest.TestCase):
    def test_server_band_thresholds(self):
        self.assertEqual(validation.band({'bull': 70, 'bear': 20}), 'high')
        self.assertEqual(validation.band({'bull': 69.9, 'bear': 40}), 'mid')
        self.assertEqual(validation.band({'bull': 39.9, 'bear': 0}), 'low')

    def test_directional_hit_and_market_return_are_separate(self):
        short_win = {'bias': 'short', 'ret_4h': 0.02, 'analyzed_at': 1, 'bench_4h': 0.10}
        self.assertEqual(validation.stats([short_win], 'ret_4h', 'bench_4h')[:2], (1, 1.0))

    def test_dedup_keeps_first_call_in_each_rolling_four_hours(self):
        def row(at, bias='short'):
            return {'symbol': 'BTCUSDT', 'bias': bias, 'analyzed_at': at}
        got = validation.dedup([row(14_400_000), row(1), row(3_599_999), row(3_600_000, 'long')])
        self.assertEqual([(r['bias'], r['analyzed_at']) for r in got], [
            ('short', 1), ('long', 3_600_000)
        ])


if __name__ == '__main__':
    unittest.main()
