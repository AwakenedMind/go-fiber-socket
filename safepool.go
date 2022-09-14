package fibersocket

import (
	"sync"

	"github.com/google/uuid"
)

type safePool struct {
	sync.RWMutex
	conn map[uuid.UUID]Fiber
}

func (s *safePool) New(f Fiber) {
	s.Lock()
	s.conn[f.GetUUID()] = f
	s.Unlock()
}

func (s *safePool) GetAll() map[uuid.UUID]Fiber {
	s.RLock()

	ret := make(map[uuid.UUID]Fiber, 0)

	for u, kFiber := range s.conn {
		ret[u] = kFiber
	}
	s.RUnlock()
	return ret
}

func (s *safePool) Get(key uuid.UUID) Fiber {
	s.RLock()
	ret, ok := s.conn[key]
	s.RUnlock()
	if !ok {
		panic("not found")
	}
	return ret
}

func (s *safePool) DoesContain(key uuid.UUID) bool {
	s.RLock()
	_, ok := s.conn[key]
	s.RUnlock()
	return ok
}

func (s *safePool) Delete(key uuid.UUID) {
	s.Lock()
	delete(s.conn, key)
	s.Unlock()
}
