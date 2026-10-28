module github.com/hansjone/auriga/deploy/compose/bootstrap

go 1.27.0

require (
	github.com/hansjone/auriga/deploy/bootstrap v0.0.0
	github.com/hansjone/auriga/infra/gen v0.0.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/nats-io/nkeys v0.4.15 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	google.golang.org/grpc v1.84.0 // indirect
)

replace github.com/hansjone/auriga/infra/gen => ../../../infra/gen

replace github.com/hansjone/auriga/deploy/bootstrap => ../../bootstrap
