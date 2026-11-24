import unittest
from trader_fresh_holdout import context
class ContextTests(unittest.TestCase):
 def bars(self,n=300):
  return [dict(Time=i*300,Close=100+i*.01,High=101+i*.01,Low=99+i*.01,QuoteVolume=1e6) for i in range(n)]
 def test_never_uses_open_candle_or_future_volume(self):
  b=self.bars();at=288*300000
  x=context(b,at,'long')
  self.assertTrue(x['trend']);self.assertEqual(x['volume24h'],288e6)
  for r in b[288:]:r['Close']=1;r['QuoteVolume']=1e12
  self.assertEqual(context(b,at,'long'),x)
 def test_incomplete_volume_is_unknown(self):
  x=context(self.bars(60),60*300000,'long')
  self.assertIsNone(x['volume24h'])
 def test_gap_rejected(self):
  b=self.bars(60);b[-10]['Time']+=1
  self.assertIsNone(context(b,60*300000,'long'))
if __name__=='__main__':unittest.main()
