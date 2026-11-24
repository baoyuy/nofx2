# Four-hour directional shortlist research

Purpose: select at most ten coins per side from followed traders' positions,
respecting the existing AI volume threshold. Independent research endpoint
`GET /copytrade/direction-shortlist`; no strategy execution integration.

Provisional gates (not optimized or advertised as predictive probabilities):
- Position and fill sources updated within 15 minutes.
- Binance USDT-M quote volume meets the saved threshold; missing volume fails closed.
- AI reading within saved TTL; AI, style reading and recent-call direction agree.
- At least two clustered calls and effective independent calls >= 2.
- For each source separately: same-side nonoverlapping mature 4h samples >= 12,
  Wilson lower bound > 50%, mean directional return > assumed round-trip cost 0.2%.
- Completed 15m bars confirm both 1h and 4h direction; no missing intervals.
- Rank by weaker historical Wilson lower bound; never label this a next-trade probability.
- Maximum twenty market checks per request; deferred work exposed in breakdown.

Read requests preserve the first observation per user/hour in
`copytrade_direction_research`, including empty boards. Later reads are live
research previews and may differ from that first observation. No background
selection occurs when nobody opens the page. Outcome loop evaluates recorded
boards after four hours. Stored results include signed endpoint return, assumed
net return and maximum adverse move over complete 15m intervals.
The initial partial candle is excluded from adverse-move measurement and endpoint
is the last completed candle at/before four hours (up to 15 minutes early).
Neither is an exact four-hour tick replay. No records are silently marked complete
if candles are missing. History older than the exchange retrieval window remains
pending for a future archive-backed evaluator.

Existing AI pool, stable leaderboard and program-strategy inputs remain separate.
The UI puts the research shortlist first and collapses the existing trading pool.

Validation: `tools/direction_shortlist_audit.py DATABASE --out REPORT` uses read-only
SQLite. It audits existing recorded AI and holdings signals; does not claim to
replay unavailable historical volume/config/style snapshots or prove the new
shortlist beats momentum. Need sufficient forward observations, exact price
alignment, adverse-excursion validation, and an identical-time momentum baseline
before any promotion to automated execution. Per-trader style-specific historical
calibration is still an existing-system limitation, not solved by this shortlist.
