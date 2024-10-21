package internal

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

const (
	DefaultReadTimeout       time.Duration = 5 * time.Second
	DefaultReadHeaderTimeout time.Duration = 5 * time.Second
	DefaultWriteTimeout      time.Duration = 10 * time.Second
	DefaultIdleTimeout       time.Duration = 120 * time.Second
	DefaultMaxHeaderBytes    int           = 1 << 20
)

type Bind struct {
	Protocol          string        `json:"protocol,omitempty"`
	RedirectToHttps   bool          `json:"redirectToHttps,omitempty"`
	Address           string        `json:"address"`
	VirtualHost       bool          `json:"virtualHost"`
	SSL               []*SSL        `json:"ssl,omitempty"`
	Groups            []*Group      `json:"groups"`
	ReadTimeout       string        `json:"readTimout,omitempty"`
	ReadHeaderTimeout string        `json:"readHeaderTimout,omitempty"`
	WriteTimeout      string        `json:"writeTimout,omitempty"`
	IdleTimeout       string        `json:"idleTimout,omitempty"`
	MaxHeaderBytes    int           `json:"maxHeaderBytes,omitempty"`
	Http12Server      *http.Server  `json:"-"`
	Http3Server       *http3.Server `json:"-"`
}

type SSL struct {
	//as input from json must be a file name, during start this will be changed as filePath by appending basePath + fileName
	CertFilePath string `json:"certFileName,omitempty"`
	KeyFilePath  string `json:"keyFileName,omitempty"`
}

func grabGroup(g []*Group, r *http.Request) (*Group, error) {
	var group *Group
	hostname, _, _ := strings.Cut(r.Host, ":")
	groupLen := len(g)
	for i := 0; i < groupLen; i++ {
		currentGroup := g[i]

		if currentGroup.Address == hostname && currentGroup.isPathCompliant(r) {
			group = currentGroup
		}
	}

	if group != nil {
		return group, nil
	}

	return nil, errors.New("group not found")
}

func (bind *Bind) reverseproxyHandler(w http.ResponseWriter, r *http.Request) {
	panicked := catchUnwind(func() {
		if bind.VirtualHost {
			group, e := grabGroup(bind.Groups, r)
			if e == nil {
				group.handleRequest(w, r)
			} else {
				http.Error(w, "Service not available", http.StatusServiceUnavailable)
			}
		} else {
			//pick only the first one, with domain resolution to false every listener is
			//directly connected to endopints without hostname distinction
			firstGroup := bind.Groups[0]
			if !firstGroup.isPathCompliant(r) {
				slog.Debug("path not compliant with the only group running")
				http.Error(w, "Service not available", http.StatusServiceUnavailable)
			} else {
				firstGroup.handleRequest(w, r)
			}
		}
	})

	if panicked {
		http.Error(w, "Service not available", http.StatusInternalServerError)
	}
}

// Generic helper function to get a value, defaulting if not set
func getWithDefaultInt(value int, defaultValue int) int {
	if value == 0 {
		return defaultValue
	}
	return value
}

func getWithDefaultDuration(durationStr string, defaultValue time.Duration) time.Duration {
	if durationStr == "" {
		return defaultValue
	}

	dur, err := time.ParseDuration(durationStr)

	if err != nil {
		slog.Error("unparsable duration", "duration", err)
		return defaultValue
	}

	return dur
}

func (bind *Bind) generateServerTLS() []tls.Certificate {
	sslSize := len(bind.SSL)
	certs := make([]tls.Certificate, sslSize)
	for i := range sslSize {
		currSSL := bind.SSL[i]
		var err error
		certs[i], err = tls.LoadX509KeyPair(currSSL.CertFilePath, currSSL.KeyFilePath)
		if err != nil {
			slog.Error("error during tls sni cert and key", "error", err)
		}
	}
	return certs
}

func (bind *Bind) Start() error {

	for _, group := range bind.Groups {
		if err := group.Start(); err != nil {
			slog.Error("error initializing group", "error", err)
		}
	}

	for i := range bind.SSL {
		basePath := runningConf.BasePath
		bind.SSL[i].KeyFilePath = path.Join(basePath, bind.SSL[i].KeyFilePath)
		bind.SSL[i].CertFilePath = path.Join(basePath, bind.SSL[i].CertFilePath)
	}

	bind.Protocol = strings.ToUpper(bind.Protocol)
	switch bind.Protocol {
	case "HTTP/2":
		{
			if bind.SSL == nil {
				return errors.New("cannot start HTTP/2 without SSL certificate")
			}

			bind.Http12Server =
				&http.Server{
					Addr:              bind.Address,
					TLSConfig:         &tls.Config{Certificates: bind.generateServerTLS()},
					ReadTimeout:       getWithDefaultDuration(bind.ReadTimeout, DefaultReadTimeout),
					ReadHeaderTimeout: getWithDefaultDuration(bind.ReadHeaderTimeout, DefaultReadHeaderTimeout),
					WriteTimeout:      getWithDefaultDuration(bind.WriteTimeout, DefaultWriteTimeout),
					IdleTimeout:       getWithDefaultDuration(bind.IdleTimeout, DefaultIdleTimeout),
					MaxHeaderBytes:    getWithDefaultInt(bind.MaxHeaderBytes, DefaultMaxHeaderBytes),
					Handler:           http.HandlerFunc(bind.reverseproxyHandler),
				}

			go func() {
				err := bind.Http12Server.ListenAndServeTLS("", "")
				if err != nil {
					slog.Error("Unable to start binding", "error", err)
				}
			}()
			break
		}
	case "HTTP/3":
		{
			if bind.SSL == nil {
				return errors.New("cannot start HTTP/3 without SSL certificate")
			}

			bind.Http12Server =
				&http.Server{
					Addr:              bind.Address,
					TLSConfig:         &tls.Config{Certificates: bind.generateServerTLS()},
					ReadTimeout:       getWithDefaultDuration(bind.ReadTimeout, DefaultReadTimeout),
					ReadHeaderTimeout: getWithDefaultDuration(bind.ReadHeaderTimeout, DefaultReadHeaderTimeout),
					WriteTimeout:      getWithDefaultDuration(bind.WriteTimeout, DefaultWriteTimeout),
					IdleTimeout:       getWithDefaultDuration(bind.IdleTimeout, DefaultIdleTimeout),
					MaxHeaderBytes:    getWithDefaultInt(bind.MaxHeaderBytes, DefaultMaxHeaderBytes),
					Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						bind.Http3Server.SetQUICHeaders(w.Header())
						bind.reverseproxyHandler(w, r)
					}),
				}

			bind.Http3Server = &http3.Server{
				Addr:           bind.Address,
				TLSConfig:      &tls.Config{Certificates: bind.generateServerTLS()},
				IdleTimeout:    getWithDefaultDuration(bind.IdleTimeout, DefaultIdleTimeout),
				MaxHeaderBytes: getWithDefaultInt(bind.MaxHeaderBytes, DefaultMaxHeaderBytes),
				QUICConfig:     &quic.Config{Allow0RTT: true},
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					bind.Http3Server.SetQUICHeaders(w.Header())
					bind.reverseproxyHandler(w, r)
				}),
			}

			httpErr := make(chan error, 1)
			quicErr := make(chan error, 1)
			go func() {
				quicErr <- bind.Http3Server.ListenAndServe()
			}()
			go func() {
				httpErr <- bind.Http12Server.ListenAndServeTLS("", "")
			}()

			go func() {
				select {
				case err := <-httpErr:
					bind.Http3Server.Close()
					slog.Error("", "error", err)
				case err := <-quicErr:
					// Cannot close the HTTP server or wait for requests to complete properly
					slog.Error("", "error", err)
				}
			}()
		}
	default:
		{
			bind.Http12Server =
				&http.Server{
					Addr:              bind.Address,
					TLSNextProto:      nil,
					TLSConfig:         &tls.Config{Certificates: bind.generateServerTLS()},
					ReadTimeout:       getWithDefaultDuration(bind.ReadTimeout, DefaultReadTimeout),
					ReadHeaderTimeout: getWithDefaultDuration(bind.ReadHeaderTimeout, DefaultReadHeaderTimeout),
					WriteTimeout:      getWithDefaultDuration(bind.WriteTimeout, DefaultWriteTimeout),
					IdleTimeout:       getWithDefaultDuration(bind.IdleTimeout, DefaultIdleTimeout),
					MaxHeaderBytes:    getWithDefaultInt(bind.MaxHeaderBytes, DefaultMaxHeaderBytes),
					Handler:           http.HandlerFunc(bind.reverseproxyHandler),
				}

			go func() {
				var err error
				if bind.Http12Server.TLSConfig == nil || len(bind.Http12Server.TLSConfig.Certificates) == 0 {
					err = bind.Http12Server.ListenAndServe()
				} else {
					err = bind.Http12Server.ListenAndServeTLS("", "")
				}
				if err != nil {
					slog.Error("Unable to start binding", "error", err)
				}
			}()
		}
	}

	return nil
}

func (bind *Bind) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if bind.Http12Server != nil {
		if err := bind.Http12Server.Shutdown(ctx); err != nil {
			return err
		}
	}

	if bind.Http3Server != nil {
		if err := bind.Http3Server.CloseGracefully(5 * time.Second); err != nil {
			return err
		}
	}

	return nil
}
