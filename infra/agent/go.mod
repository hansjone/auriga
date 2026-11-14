module github.com/hansjone/auriga/infra/agent

go 1.27.0

require (
	github.com/Microsoft/go-winio v0.6.2
	github.com/hansjone/auriga/infra/edge v0.0.0
	github.com/hansjone/auriga/infra/gen v0.0.0
	github.com/robfig/cron/v3 v3.0.1
	go.etcd.io/bbolt v1.4.3
	golang.org/x/crypto v0.57.0
	golang.org/x/sys v0.48.0
	golang.org/x/text v0.42.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	golang.org/x/net v0.60.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260706201446-f0a921348800 // indirect
	gopkg.in/validator.v2 v2.0.1 // indirect
)

replace github.com/hansjone/auriga/infra/gen => ../gen

replace github.com/hansjone/auriga/infra/edge => ../edge
