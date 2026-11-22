//go:build windows

package main

import rt "jieshangai/client/runtime"

func runPS1(body []byte, env ...string) (string, error) { return rt.RunPS1(body, env...) }
