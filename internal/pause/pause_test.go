package pause

import (
	"context"
	"testing"
	"time"
)

// The real wait is what bounds a retry against the command's budget, so a
// context that is already done must not buy another interval.
func TestForStopsWhenTheContextDoes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	started := time.Now()
	if err := For(ctx, time.Hour); err == nil {
		t.Fatal("For() error = nil, want the context's own")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("For() took %v, want to stop with the context", elapsed)
	}
}

func TestOrFallsBackToTheRealWaitOnlyWhenUnset(t *testing.T) {
	called := false
	var own Func = func(context.Context, time.Duration) error { called = true; return nil }
	if err := Or(own)(context.Background(), time.Hour); err != nil || !called {
		t.Errorf("Or(own) did not call own: err=%v called=%t", err, called)
	}
	if err := Or(nil)(context.Background(), time.Millisecond); err != nil {
		t.Errorf("Or(nil) = %v, want the real wait to finish", err)
	}
}
