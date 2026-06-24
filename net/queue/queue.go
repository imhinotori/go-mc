package queue

import (
	"container/list"
	"sync"
)

type Queue[T any] interface {
	Push(v T) (ok bool)
	Pull() (v T, ok bool)
	Close()
}

func NewLinkedQueue[T any]() (q Queue[T]) {
	return &LinkedListQueue[T]{
		queue: list.New(),
		cond:  sync.Cond{L: new(sync.Mutex)},
	}
}

type LinkedListQueue[T any] struct {
	queue  *list.List
	closed bool
	cond   sync.Cond
}

func (p *LinkedListQueue[T]) Push(v T) bool {
	p.cond.L.Lock()
	if p.closed {
		panic("push on closed queue")
	}
	p.queue.PushBack(v)
	p.cond.Signal()
	p.cond.L.Unlock()
	return true
}

func (p *LinkedListQueue[T]) Pull() (v T, ok bool) {
	p.cond.L.Lock()
	for {
		if elem := p.queue.Front(); elem != nil {
			v = p.queue.Remove(elem).(T)
			ok = true
			break
		} else if p.closed {
			break
		}
		p.cond.Wait()
	}
	p.cond.L.Unlock()
	return
}

func (p *LinkedListQueue[T]) Close() {
	p.cond.L.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.cond.L.Unlock()
}

func NewChannelQueue[T any](n int) (q Queue[T]) {
	return &ChannelQueue[T]{ch: make(chan T, n)}
}

// ChannelQueue is a bounded, non-blocking MPSC queue backed by a buffered channel.
// Push is a non-blocking try-send (drops when full); Pull blocks until an item is
// available or the queue is closed.
//
// CONCURRENCY: a bare `close(ch)` racing a `ch <- v` is a data race (and panics with
// "send on closed channel"). Multiple producers (e.g. the tick's flushOutbound and a
// readLoop-triggered Close) touch this queue, so Close and Push are serialized by a
// mutex + a `closed` flag. Once closed, Push is a no-op returning false (the connection
// is going away) and Close is idempotent. Pull still reads the channel without the lock:
// the buffered channel is itself safe for a single concurrent reader, and a closed
// channel drains its buffer then reports ok=false — which is exactly the writeLoop's
// stop signal. This makes -race clean under concurrent Close/Push (the Phase-5 join-seam
// race the capture-diff load surfaced).
type ChannelQueue[T any] struct {
	ch     chan T
	mu     sync.Mutex
	closed bool
}

func (c *ChannelQueue[T]) Push(v T) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false // queue going away: drop, never send on a closed channel
	}
	select {
	case c.ch <- v:
		return true
	default:
		return false // full: caller's bounded drop-and-disconnect policy applies
	}
}

func (c *ChannelQueue[T]) Pull() (v T, ok bool) {
	v, ok = <-c.ch
	return
}

func (c *ChannelQueue[T]) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return // idempotent
	}
	c.closed = true
	close(c.ch)
}
