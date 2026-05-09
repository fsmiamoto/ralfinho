package agent

import (
	"sync"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

func collectEvents() (func(events.Event), func() []events.Event) {
	var mu sync.Mutex
	var evts []events.Event
	onEvent := func(ev events.Event) {
		mu.Lock()
		evts = append(evts, ev)
		mu.Unlock()
	}
	get := func() []events.Event {
		mu.Lock()
		defer mu.Unlock()
		result := make([]events.Event, len(evts))
		copy(result, evts)
		return result
	}
	return onEvent, get
}
