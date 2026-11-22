module jieshangai/client/app

go 1.26.0

require (
	github.com/ebitengine/purego v0.11.1
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	golang.org/x/sys v0.49.0
	jieshangai/client/runtime v0.0.0
)

require (
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	jieshangai/client/core v0.0.0 // indirect
)

replace jieshangai/client/runtime => ../runtime

replace jieshangai/client/core => ../core
