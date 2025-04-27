package registrar

import (
	"sync"
)

type Registrar struct {
	values map[string]interface{}
	mutex  sync.RWMutex
}

// Initializes a new registrar
func New() *Registrar {
	return &Registrar{
		values: make(map[string]interface{}),
	}
}

// Set a key pair value in the registrar
func (r *Registrar) Set(k string, v interface{}) {
	if r == nil {
		return
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.values[k] = v
}

// Get a value for a given key from the registrar
func (r *Registrar) Get(k string) interface{} {
	if r == nil {
		return nil
	}

	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return r.values[k]
}
