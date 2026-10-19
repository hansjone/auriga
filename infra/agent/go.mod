module github.com/hansjone/auriga/infra/agent

go 1.27.0

require (
	github.com/hansjone/auriga/infra/gen v0.0.0
	go.etcd.io/bbolt v1.4.3
	golang.org/x/crypto v0.42.0
	google.golang.org/grpc v1.76.0
	google.golang.org/protobuf v1.36.10
)

require (
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250804133106-a7a43d27e69b // indirect
)

replace github.com/hansjone/auriga/infra/gen => ../gen
