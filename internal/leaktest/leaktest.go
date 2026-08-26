package leaktest

import (
	"testing"

	"go.uber.org/goleak"
)

// Options returns goleak options that ignore this proxy's long-lived
// background goroutines (started lazily), which are not leaks.
func Options(extra ...goleak.Option) []goleak.Option {
	base := []goleak.Option{
		goleak.IgnoreTopFunction("github.com/NovikovRoman/b4lite/internal/log.startFlusherLocked.func1"),
	}
	return append(base, extra...)
}

func VerifyTestMain(m *testing.M, extra ...goleak.Option) {
	goleak.VerifyTestMain(m, Options(extra...)...)
}
