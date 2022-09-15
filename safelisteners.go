package fibersocket

import "sync"

type SafeListeners struct {
	sync.RWMutex
	list map[string][]eventCallback
}

func (l *SafeListeners) set(event string, callback eventCallback) {
	l.Lock()
	listeners.list[event] = append(listeners.list[event], callback)
	l.Unlock()
}

// Safely returns an event callback from the safe pool list
func (l *SafeListeners) get(event string) []eventCallback {
	l.RLock()
	defer l.RUnlock()
	if _, ok := l.list[event]; !ok {
		return make([]eventCallback, 0)
	}

	ret := make([]eventCallback, 0)
	for _, v := range l.list[event] {
		ret = append(ret, v)
	}
	return ret
}
