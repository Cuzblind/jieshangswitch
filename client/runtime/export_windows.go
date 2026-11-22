//go:build windows

package clientruntime

func RunPS1(body []byte, env ...string) (string, error) { return runPS1(body, env...) }
