package pubsub

import "sync"

// Broker is a tiny in-memory pub/sub used to fan out new messages to connected sessions.
// v1 keeps this in-memory; persistence is in SQLite.
type Broker[T any] struct {
	mu   sync.Mutex
	subs map[int]chan T
	next int
}

func New[T any]() *Broker[T] {
	return &Broker[T]{subs: make(map[int]chan T)}
}

func (b *Broker[T]) Subscribe(buf int) (id int, ch <-chan T, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id = b.next
	b.next++
	c := make(chan T, buf)
	b.subs[id] = c

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if c2, ok := b.subs[id]; ok {
				delete(b.subs, id)
				close(c2)
			}
		})
	}

	return id, c, cancel
}

func (b *Broker[T]) Publish(v T) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- v:
		default:
			// Drop if subscriber is slow; UI will refresh on demand.
		}
	}
}
