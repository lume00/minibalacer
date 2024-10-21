package internal

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
)

type StatelessSessionPersistence struct {
	CookieName     string   `json:"cookieName"`
	CookieSettings []string `json:"cookieSettings"`
}

func (s StatelessSessionPersistence) getStatelessPersistentSessionCookieName() string {
	if s.CookieName == "" {
		return "_gbsps"
	}

	return s.CookieName
}

func (s StatelessSessionPersistence) getCookie(r *http.Request) (string, error) {
	var cookieVal string
	for _, c := range r.Cookies() {
		if c.Name == s.getStatelessPersistentSessionCookieName() {
			cookieVal = c.Value
			break
		}
	}

	if cookieVal != "" {
		return cookieVal, nil
	}
	return "", errors.New("no persistent session cookie found")
}

func (s StatelessSessionPersistence) setCookie(w http.ResponseWriter, group *Group, endpoint *Endpoint) {
	var buffer bytes.Buffer

	buffer.WriteString(s.getStatelessPersistentSessionCookieName())
	buffer.WriteString("=")
	buffer.WriteString(endpoint.Signature)

	if group.Path != "" {
		buffer.WriteString("; Path=")
		buffer.WriteString(group.Path)
	}

	if len(s.CookieSettings) != 0 {
		buffer.WriteString("; ")
		buffer.WriteString(strings.Join(s.CookieSettings[:], "; "))
	}

	w.Header().Add("Set-Cookie", buffer.String())
}

func (s StatelessSessionPersistence) get(r *http.Request, endpoints []*Endpoint) (*Endpoint, error) {
	cookieval, e := s.getCookie(r)
	if e != nil {
		return nil, e
	}

	for _, endp := range endpoints {
		if endp.Signature == cookieval {
			return endp, nil
		}
	}

	return nil, errors.New("no persistent session cookie found: all signatures missmatched")
}
