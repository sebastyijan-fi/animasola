package pubsub

import "testing"

func TestCancelIdempotent(t *testing.T) {
	b := New[int]()
	_, _, cancel := b.Subscribe(1)
	cancel()
	cancel()
}
