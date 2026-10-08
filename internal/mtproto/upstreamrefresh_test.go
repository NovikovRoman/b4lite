package mtproto

import (
	"errors"
	"testing"
	"time"

	"github.com/NovikovRoman/b4lite/internal/config"
)

type upstreamRefreshCalls struct {
	dcs []string
	cf  []string
}

func upstreamRefreshTestEnv(t *testing.T) *upstreamRefreshCalls {
	t.Helper()
	prevDCs, prevCF := upstreamRefreshDCs, upstreamRefreshCF
	t.Cleanup(func() { upstreamRefreshDCs, upstreamRefreshCF = prevDCs, prevCF })
	calls := &upstreamRefreshCalls{}
	upstreamRefreshDCs = func(_ bool, url string) error {
		calls.dcs = append(calls.dcs, url)
		return nil
	}
	upstreamRefreshCF = func(url string) (int, error) {
		calls.cf = append(calls.cf, url)
		return 5, nil
	}
	return calls
}

func TestUpstreamRefreshStaysOfflineWhileTelegramIsUnused(t *testing.T) {
	calls := upstreamRefreshTestEnv(t)
	cfg := newTestConfig()
	var st upstreamRefreshState
	now := time.Unix(1_800_000_000, 0)

	if next := st.step(&cfg, now); next != 0 || len(calls.dcs)+len(calls.cf) != 0 {
		t.Fatalf("with every Telegram feature off nothing may be fetched, got dcs=%v cf=%v next=%v", calls.dcs, calls.cf, next)
	}

	cfg.System.MTProto.Enabled = true
	if !cfg.TelegramInUse() {
		t.Fatal("an enabled MTProto proxy uses Telegram")
	}
}

func TestUpstreamRefreshFetchesOnceWhenTelegramIsTurnedOn(t *testing.T) {
	calls := upstreamRefreshTestEnv(t)
	cfg := newTestConfig()
	var st upstreamRefreshState
	now := time.Unix(1_800_000_000, 0)
	st.step(&cfg, now)

	cfg.System.MTProto.Enabled = true
	next := st.step(&cfg, now)
	if len(calls.dcs) != 1 || len(calls.cf) != 1 {
		t.Fatalf("turning the proxy on must fetch both lists once, got dcs=%v cf=%v", calls.dcs, calls.cf)
	}
	if next != cfProxyRefreshInt {
		t.Errorf("next wake %v, want the hourly refresh", next)
	}

	if st.step(&cfg, now.Add(time.Minute)); len(calls.dcs) != 1 || len(calls.cf) != 1 {
		t.Errorf("an unrelated config push must not fetch again, got dcs=%v cf=%v", calls.dcs, calls.cf)
	}

	st.step(&cfg, now.Add(cfProxyRefreshInt))
	if len(calls.dcs) != 1 || len(calls.cf) != 2 {
		t.Errorf("after an hour only the CF list is refreshed, got dcs=%v cf=%v", calls.dcs, calls.cf)
	}

	cfg.System.MTProto.DCFallbackURL = "https://mirror.example/getProxyConfig"
	cfg.System.MTProto.CFProxyURL = "https://mirror.example/cf.txt"
	st.step(&cfg, now.Add(cfProxyRefreshInt+time.Minute))
	if len(calls.dcs) != 2 || len(calls.cf) != 3 || calls.cf[2] != "https://mirror.example/cf.txt" {
		t.Errorf("a new source URL must be fetched at once, got dcs=%v cf=%v", calls.dcs, calls.cf)
	}

	cfg.System.MTProto.CFProxyEnabled = false
	if next := st.step(&cfg, now.Add(2*cfProxyRefreshInt)); next != 0 || len(calls.cf) != 3 {
		t.Errorf("with the CF fallback off the list is not refreshed, got cf=%v next=%v", calls.cf, next)
	}

	cfg.System.MTProto.Enabled = false
	st.step(&cfg, now.Add(3*cfProxyRefreshInt))
	cfg.System.MTProto.Enabled = true
	cfg.System.MTProto.CFProxyEnabled = true
	st.step(&cfg, now.Add(3*cfProxyRefreshInt))
	if len(calls.dcs) != 3 || len(calls.cf) != 4 {
		t.Errorf("turning Telegram off and on again must fetch again, got dcs=%v cf=%v", calls.dcs, calls.cf)
	}
}

func TestUpstreamRefreshRetriesAFailedDownload(t *testing.T) {
	calls := upstreamRefreshTestEnv(t)
	dcErr, cfErr := errors.New("network is unreachable"), errors.New("network is unreachable")
	upstreamRefreshDCs = func(_ bool, url string) error {
		calls.dcs = append(calls.dcs, url)
		return dcErr
	}
	upstreamRefreshCF = func(url string) (int, error) {
		calls.cf = append(calls.cf, url)
		return 5, cfErr
	}
	cfg := newTestConfig()
	cfg.System.MTProto.Enabled = true
	cfg.System.MTProto.CFProxyEnabled = false
	var st upstreamRefreshState
	now := time.Unix(1_800_000_000, 0)

	if next := st.step(&cfg, now); next != upstreamRefreshRetryBase || len(calls.dcs) != 1 {
		t.Fatalf("a failed DC download at start must be retried after %v even with the CF fallback off, got next=%v dcs=%d", upstreamRefreshRetryBase, next, len(calls.dcs))
	}
	if st.step(&cfg, now.Add(10*time.Second)); len(calls.dcs) != 1 {
		t.Errorf("a save before the retry is due must not download again, got %d downloads", len(calls.dcs))
	}
	if next := st.step(&cfg, now.Add(upstreamRefreshRetryBase)); next != 2*upstreamRefreshRetryBase || len(calls.dcs) != 2 {
		t.Errorf("the second failure must double the wait, got next=%v dcs=%d", next, len(calls.dcs))
	}

	cfg.System.MTProto.DCFallbackURL = "https://mirror.example/getProxyConfig"
	if next := st.step(&cfg, now.Add(time.Minute)); next != upstreamRefreshRetryBase || len(calls.dcs) != 3 {
		t.Errorf("a new source must be tried at once and restart the backoff, got next=%v dcs=%d", next, len(calls.dcs))
	}

	dcErr = nil
	if next := st.step(&cfg, now.Add(time.Minute+upstreamRefreshRetryBase)); next != 0 || len(calls.dcs) != 4 {
		t.Errorf("a success must clear the retry, got next=%v dcs=%d", next, len(calls.dcs))
	}

	cfg.System.MTProto.CFProxyEnabled = true
	base := now.Add(time.Hour)
	if next := st.step(&cfg, base); next != upstreamRefreshRetryBase || len(calls.cf) != 1 {
		t.Fatalf("a failed CF download must be retried after %v, not an hour, got next=%v cf=%d", upstreamRefreshRetryBase, next, len(calls.cf))
	}
	cfErr = nil
	if next := st.step(&cfg, base.Add(upstreamRefreshRetryBase)); next != cfProxyRefreshInt || len(calls.cf) != 2 || len(calls.dcs) != 4 {
		t.Errorf("after the CF retry succeeds only the hourly refresh remains, got next=%v cf=%d dcs=%d", next, len(calls.cf), len(calls.dcs))
	}
}

// newTestConfig stands in for upstream's config.NewConfig: the defaults with
// every Telegram feature off.
func newTestConfig() config.Config {
	cfg := config.DefaultConfig
	cfg.System.MTProto.Enabled = false
	return cfg
}
