import importlib.util
import sqlite3
import unittest
from pathlib import Path


spec = importlib.util.spec_from_file_location(
    'copytrade_backtest', Path(__file__).with_name('copytrade_backtest.py')
)
backtest = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backtest)


class CopyTradeBacktestTest(unittest.TestCase):
    def test_ai_return_is_already_direction_adjusted(self):
        # A short call whose underlying coin fell is stored as positive.
        self.assertGreater(backtest.direction_adjusted_return({'ret_4h': 0.03}), 0)
        self.assertLess(backtest.direction_adjusted_return({'ret_4h': -0.02}), 0)

    def test_ai_dedup_matches_first_same_coin_side_call_in_four_hours(self):
        def row(symbol, bias, at):
            return {'symbol': symbol, 'bias': bias, 'analyzed_at': at}

        kept = backtest.dedup_ai_outcomes([
            row('BTCUSDT', 'short', 0),
            row('BTCUSDT', 'short', 3_599_999),
            row('BTCUSDT', 'long', 3_600_000),
            row('BTCUSDT', 'short', 14_400_000),
            row('BTCUSDT', 'neutral', 14_400_001),
        ])
        self.assertEqual([(r['bias'], r['analyzed_at']) for r in kept], [
            ('short', 0), ('long', 3_600_000), ('short', 14_400_000)
        ])

    def test_dedup_sorts_before_selecting_first_call(self):
        rows = [
            {'symbol': 'ETHUSDT', 'bias': 'long', 'analyzed_at': 3_600_000},
            {'symbol': 'ETHUSDT', 'bias': 'long', 'analyzed_at': 0},
        ]
        self.assertEqual(backtest.dedup_ai_outcomes(rows)[0]['analyzed_at'], 0)


if __name__ == '__main__':
    unittest.main()
