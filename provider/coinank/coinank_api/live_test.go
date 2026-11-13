package coinank_api

import (
	"os"
	"testing"
)

// requireLive skips tests that call the real CoinAnk API unless COINANK_LIVE=1
// is set. They are not part of the offline suite: a slow or blocked network
// makes them fail for reasons unrelated to the code.
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("COINANK_LIVE") == "" {
		t.Skip("live CoinAnk API test; set COINANK_LIVE=1 to run")
	}
}
