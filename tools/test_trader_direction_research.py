import unittest
from trader_direction_research import classify,outcome,metrics

class ResearchTests(unittest.TestCase):
 def event(self,at,action,price=100,qty=1,side='short'):
  return dict(time=at,symbol='X',side=side,action=action,price=price,qty=qty)
 def test_add_uses_prior_cost_and_first_open_is_not_proven_new(self):
  events=[self.event(1,'open'),self.event(2,'add',110),self.event(3,'add',90),self.event(4,'close'),self.event(5,'open')]
  self.assertEqual([e['category'] for e in classify(events)],['first_observed_open','adverse_add','trend_add','close','new_open'])
 def test_delay_direction_and_path(self):
  times=list(range(300,15100,300));prices=[100]*len(times);prices[3]=103;prices[49]=95
  row=outcome(self.event(0,'open'),(times,prices))
  self.assertIsNotNone(row)
  # at least 5m delay starts at the closed bar at t=300, endpoint t=14700
  self.assertEqual(row['entry_at'],300000)
  self.assertAlmostEqual(row['adverse_close_4h'],.03)
  self.assertFalse(row['no_1pct_close_reversal'])
 def test_missing_path_is_rejected(self):
  times=list(range(300,16000,300));prices=[100]*len(times)
  del times[20];del prices[20]
  self.assertIsNone(outcome(self.event(0,'open'),(times,prices)))
 def test_flat_counts_as_miss(self):
  rows=[dict(returns={'14400':0},adverse_close_4h=0)]
  self.assertEqual(metrics(rows)['n'],1)
  self.assertEqual(metrics(rows)['hit'],0)
if __name__=='__main__':unittest.main()
