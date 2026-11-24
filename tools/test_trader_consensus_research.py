import unittest
from trader_consensus_research import summarize
class ConsensusTests(unittest.TestCase):
 def test_smooth_success_requires_both_profit_and_limited_reversal(self):
  rows=[{'symbol':'X','at':0,'returns':{'14400':.01},'adverse_close_4h':.02},
        {'symbol':'X','at':1,'returns':{'14400':-.01},'adverse_close_4h':0},
        {'symbol':'Y','at':86400000,'returns':{'14400':.01},'adverse_close_4h':.005}]
  m=summarize(rows)
  self.assertAlmostEqual(m['smooth'],1/3)
  self.assertEqual(m['coins'],2)
  self.assertEqual(m['days'],2)
 def test_empty_is_not_success(self):
  m=summarize([])
  self.assertEqual(m['n'],0)
  self.assertEqual(m['smooth'],0)
if __name__=='__main__':unittest.main()
