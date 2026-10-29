package scan

// subscriberBuffer is the capacity of each subscriber's channel. Progress is
// coalesced to about five events a second, so a reader that keeps up never
// fills it; one that falls behind loses progress events, never the end.
const subscriberBuffer = 16

// Event is one message to a scan's subscribers: a "progress" event with the
// latest Progress, or the final "end" event with the job's State (done or
// failed). The channel is closed after the end event.
type Event struct {
	Kind     string   `json:"kind"`
	Progress Progress `json:"progress"`
	State    string   `json:"state"`
}

// The event kinds.
const (
	EventProgress = "progress"
	EventEnd      = "end"
)

// broadcaster fans one job's events out to its subscribers. It is not safe on
// its own: every method is called with the Manager's mutex held, so the
// manager is the only producer and a send never races a close.
//
// A send never blocks. A progress event is sent only while the channel keeps
// one slot free (len < cap-1), and is otherwise dropped for that subscriber;
// the end event then always fits into the free slot.
type broadcaster struct {
	subs map[chan Event]struct{}
}

func newBroadcaster() *broadcaster {
	return &broadcaster{subs: map[chan Event]struct{}{}}
}

// add registers a new subscriber.
func (b *broadcaster) add() chan Event {
	ch := make(chan Event, subscriberBuffer)
	b.subs[ch] = struct{}{}
	return ch
}

// remove unregisters a subscriber and closes its channel. It is a no-op for a
// channel already removed or ended.
func (b *broadcaster) remove(ch chan Event) {
	if _, ok := b.subs[ch]; !ok {
		return
	}
	delete(b.subs, ch)
	close(ch)
}

// progress sends p to every subscriber that has room for it while keeping one
// slot free for the end event.
func (b *broadcaster) progress(p Progress) {
	for ch := range b.subs {
		if len(ch) < cap(ch)-1 {
			ch <- Event{Kind: EventProgress, Progress: p}
		}
	}
}

// end sends the end event to every subscriber, closes their channels and
// forgets them.
func (b *broadcaster) end(state string) {
	for ch := range b.subs {
		select {
		case ch <- Event{Kind: EventEnd, State: state}:
		default: // unreachable: progress always leaves a slot free
		}
		delete(b.subs, ch)
		close(ch)
	}
}
