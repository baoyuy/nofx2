// Only for endpoints backed by local monitor snapshots, never discovery scans
// or exchange requests. SWR coalesces in-flight reads and pauses hidden/offline.
export const LOCAL_SNAPSHOT_OPTIONS = {
  refreshInterval: 4000,
  dedupingInterval: 2000,
  refreshWhenHidden: false,
  refreshWhenOffline: false,
  errorRetryInterval: 15000,
  errorRetryCount: 3,
}
