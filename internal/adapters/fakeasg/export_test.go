package fakeasg

import (
	"context"
	"testing"
)

func cancelled(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx, cancel
}
