package internal

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

type balance interface {
	balanced(endpoints []*Endpoint, obj any) (*Endpoint, error)
}

const (
	ROUND_ROBIN = "roundrobin"
	FAILOVER    = "failover"
)

type Group struct {
	Address            string      `json:"address"`
	Path               string      `json:"path"`
	SessionPersistence bool        `json:"sessionPersistence"`
	Endpoints          []*Endpoint `json:"endpoints"`
	Algorithm          string      `json:"algorithm"`

	//field used for balancing function, currently only roundrobin
	balance `json:"-"`
}

func (group *Group) isPathCompliant(r *http.Request) bool {
	return group.Path == "" || strings.HasPrefix(r.URL.Path, group.Path)
}

func (group *Group) handleRequest(w http.ResponseWriter, r *http.Request) {
	endpoints := group.Endpoints
	if group.SessionPersistence {

		var chosenEndp *Endpoint
		var e error

		chosenEndp, e = runningConf.Settings.PersistentSession.get(r, endpoints)
		if e != nil || !chosenEndp.Alive.Load() {
			slog.Debug("persistent session not found or chosen endpoint is not alive")
			chosenEndp, e = group.getBalancedEndpoint(endpoints)

			if e != nil {
				http.Error(w, "Service not available", http.StatusServiceUnavailable)
				return
			}
			runningConf.Settings.PersistentSession.setCookie(w, group, chosenEndp)
		}

		slog.Debug("chosen endppoint", "endp", chosenEndp.Address)
		chosenEndp.ServeHTTP(w, r)
	} else {
		chosenEndp, e := group.getBalancedEndpoint(endpoints)
		if e != nil {
			http.Error(w, "Service not available", http.StatusServiceUnavailable)
			return
		}

		slog.Debug("chosen endppoint", "endp", chosenEndp.Address)

		chosenEndp.ServeHTTP(w, r)
	}
}

func (group *Group) getBalancedEndpoint(endpoints []*Endpoint) (*Endpoint, error) {
	switch len(endpoints) {
	case 0:
		return nil, errors.New("no endpoints available")
	case 1:
		{
			endpoint := endpoints[0]
			if endpoint.Alive.Load() {
				return endpoint, nil
			}

			return nil, errors.New("the only endpoint available is unreachable")
		}
	default:
		return group.balance.balanced(endpoints, nil)
	}
}

func (group *Group) initBalancing() {
	switch strings.ToLower(group.Algorithm) {
	case ROUND_ROBIN:
		group.balance = &roundRobin{}
	case FAILOVER:
		group.balance = &failover{}
	default:
		group.balance = &roundRobin{}
	}
}

func (group *Group) Start() error {

	//initializing load balancing algorithm
	group.initBalancing()

	for _, endpoint := range group.Endpoints {
		if e := endpoint.Start(group); e != nil {
			slog.Error("error starting endpoint", endpoint.Address, e)
		}
	}

	return nil
}
