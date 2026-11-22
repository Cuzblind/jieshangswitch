//go:build !windows

package clientruntime

import (
	"net/http"
	"net/url"
)

func systemAwareProxy(req *http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment(req)
}
