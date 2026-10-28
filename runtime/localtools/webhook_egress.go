package localtools

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

var errRedirectBlocked = errors.New("webtools: redirect refused by policy")

var errEgressBlocked = errors.New("webtools: link-local / cloud-metadata address refused")

func isBlockedEgressIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func denyLinkLocalDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedEgressIP(ip) {
		return fmt.Errorf("%w: %s", errEgressBlocked, host)
	}
	return nil
}

func newContainedWebClient(timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   denyLinkLocalDial,
	}).DialContext
	return &http.Client{Timeout: timeout, Transport: tr}
}
