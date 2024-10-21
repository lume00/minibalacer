package internal

import "errors"

type failover struct {
}

func (failover failover) balanced(endpoints []*Endpoint, _ any) (*Endpoint, error) {
	for _, e := range endpoints {
		if e.Alive.Load() {
			return e, nil
		}
	}

	return nil, errors.New("all endpoints are down")
}
