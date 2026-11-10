module github.com/sylvinus/agent-vm/mobile/bindings

go 1.26.10

require github.com/sylvinus/agent-vm v0.0.0

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/mobile v0.0.0-20261011034528-197770444867 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
)

replace github.com/sylvinus/agent-vm => ../..

tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)
