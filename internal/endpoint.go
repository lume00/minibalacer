package internal

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type contextKey int

const (
	RETRY_SAME_ENDP contextKey = iota
	RETRY_ANOTHER_ENDP
)

const maxRetry = 3

type Endpoint struct {
	Address string `json:"address"`

	TLSInsecureSkipVerify bool `json:"tlsInsecureSkipVerify"`

	// proxy parameters
	ProxyPass     string `json:"proxyPass"`
	ProxyRedirect bool   `json:"proxyRedirect"`

	ActiveConnections atomic.Uint64          `json:"-"`
	Alive             atomic.Bool            `json:"-"`
	ReverseProxy      *httputil.ReverseProxy `json:"-"`

	//used for persistent session
	Signature string `json:"-"`
}

func (endpoint *Endpoint) HealthCheck() {
	url, err := url.Parse(endpoint.Address)

	if err != nil {
		slog.Info("Error parsing healthcheck url: ", "error", err)
		return
	}

	host := url.Host
	if !strings.Contains(host, ":") {
		switch url.Scheme {
		case "http":
			host += ":80"
		case "https":
			host += ":443"
		}
	}

	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		endpoint.Alive.Store(false)
	} else {
		endpoint.Alive.Store(true)
		defer conn.Close()
	}

}

func (endpoint *Endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	endpoint.ActiveConnections.Add(1)
	endpoint.ReverseProxy.ServeHTTP(w, r)
	endpoint.ActiveConnections.Swap(endpoint.ActiveConnections.Load() - 1)
}

func getRetryFromContext(key contextKey, r *http.Request) int {
	if retry, ok := r.Context().Value(key).(int); ok {
		return retry
	}
	return 0
}

// optain the signature using the address to encrypt himself
// it useful for stateless persistent session
func sign(endpoint *Endpoint, group *Group) {
	hash := sha1.New()
	var buff bytes.Buffer
	buff.WriteString(endpoint.Address)
	if group.Path != "" {
		buff.WriteString(group.Path)
	}

	buff.WriteString(strings.Join(runningConf.Settings.PersistentSession.CookieSettings, ""))

	hash.Write(buff.Bytes())
	endpoint.Signature = base64.URLEncoding.EncodeToString(hash.Sum(nil))
}

func (endpoint *Endpoint) Start(group *Group) error {
	sign(endpoint, group)
	parsedaddress, e := url.Parse(endpoint.Address)
	if e != nil {
		return e
	}

	proxy := httputil.NewSingleHostReverseProxy(parsedaddress)

	proxy.Transport = &http.Transport{
		//todo timouts and more...
		TLSClientConfig: &tls.Config{InsecureSkipVerify: endpoint.TLSInsecureSkipVerify},
	}

	if endpoint.ProxyPass != "" {
		proxyAddress, e := url.Parse(endpoint.ProxyPass)
		if e != nil {
			return e
		}

		proxyDirectorDefault := proxy.Director

		if proxyAddress.Path == "" {
			proxyAddress.Path = "/"
		}

		proxy.Director = func(req *http.Request) {
			proxyDirectorDefault(req)

			req.Host = proxyAddress.Host
			req.URL.Host = proxyAddress.Host
			req.URL.Scheme = proxyAddress.Scheme

			toJoinPath := "/"
			if (req.URL.Path != "/" || group.Path != "/") && req.URL.Path != group.Path {
				toJoinPath = strings.Replace(req.URL.Path, group.Path, "", 1)
			}

			joinedURL, err := url.JoinPath(proxyAddress.Path, toJoinPath)

			if err != nil {
				slog.Error("error during path join", "error", err)
			}

			req.URL.Path = joinedURL
		}

		proxy.ModifyResponse = func(r *http.Response) error {
			if endpoint.ProxyRedirect {
				url, err := r.Location()

				if err != nil {
					return nil
				}

				urlLocation := url.Path

				//if relative the default is ok
				if strings.HasPrefix(urlLocation, ".") || !strings.HasPrefix(urlLocation, "/") {
					return nil
				}

				path := strings.Replace(url.Path, proxyAddress.Path, "", 1)
				if path != "" {
					r.Header.Add("Location", path)
				}
			}
			return nil
		}
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {

		if errors.Is(err, context.Canceled) {
			slog.Debug("proxy error handler raised an error", "error", err)
			return
		}

		if err != nil {
			slog.Debug("proxy error", "error", err)
		}

		retriesSameEndp := getRetryFromContext(RETRY_SAME_ENDP, r)
		if retriesSameEndp < maxRetry {
			slog.Debug("retried too many times the same endpoint", "endpoint", endpoint.Address)
			ctx := context.WithValue(r.Context(), RETRY_SAME_ENDP, retriesSameEndp+1)
			proxy.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		endpoint.Alive.Store(false)

		retriesAnotherEndp := getRetryFromContext(RETRY_ANOTHER_ENDP, r)
		if retriesAnotherEndp < maxRetry {
			ctx := context.WithValue(r.Context(), RETRY_ANOTHER_ENDP, retriesAnotherEndp+1)
			group.handleRequest(w, r.WithContext(ctx))
			return
		}

		slog.Debug("retried too many times another endpoint, giving up", "endpoint", endpoint.Address)
		http.Error(w, "Service not available", http.StatusServiceUnavailable)
	}

	endpoint.ReverseProxy = proxy

	return nil
}
