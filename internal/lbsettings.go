package internal

import (
	"log/slog"
	"time"
)

type LoadBalancerSettings struct {
	Bind                []*Bind                     `json:"bindings"`
	HealthCheckInterval string                      `json:"healthCheckInterval,omitempty"`
	healthCheckTicker   *time.Ticker                `json:"-"`
	PersistentSession   StatelessSessionPersistence `json:"sessionPersistenceDetails"`
}

// func (s *LoadBalancerSettings) addBinding(newListener *Bind) {
// 	s.Bind = append(s.Bind, newListener)
// }

// func (s *LoadBalancerSettings) removeBinding(addr string) {
// 	s.Bind = slices.DeleteFunc(s.Bind, func(oldL *Bind) bool {
// 		if oldL.ServerH12.Addr == addr {
// 			err := oldL.Stop()
// 			if err != nil {
// 				slog.Error("error during listener stop", "error", err)
// 			}
// 			return true
// 		}
// 		return false
// 	})
// }

func (s *LoadBalancerSettings) passiveHealthCheck() {

	var duration time.Duration

	if s.HealthCheckInterval == "" {
		duration, _ = time.ParseDuration("15s")
	} else {
		d, e := time.ParseDuration(s.HealthCheckInterval)
		if e != nil {
			duration, _ = time.ParseDuration("15s")
		} else {
			duration = d
		}
	}

	s.healthCheckTicker = time.NewTicker(duration)
	for range s.healthCheckTicker.C {
		slog.Debug("Passive health check started")
		s.HealthCheck()
		slog.Debug("Passive health check completed")
	}
}

func (s *LoadBalancerSettings) HealthCheck() {
	for _, listener := range s.Bind {
		for _, group := range listener.Groups {
			for _, endpoint := range group.Endpoints {
				endpoint.HealthCheck()
			}
		}
	}
}

func (s *LoadBalancerSettings) startPassiveHealthCheck() {
	s.HealthCheck()
	go s.passiveHealthCheck()
}

func (s *LoadBalancerSettings) Start() error {
	s.startPassiveHealthCheck()

	for _, listener := range s.Bind {
		err := listener.Start()
		if err != nil {
			slog.Error("error during listener start", "error", err)
		}
	}

	return nil
}

func (s *LoadBalancerSettings) Stop() {

	for _, listener := range s.Bind {
		err := listener.Stop()
		if err != nil {
			slog.Error("error stopping listener", "error", err)
		}
	}

	s.healthCheckTicker.Stop()
}
