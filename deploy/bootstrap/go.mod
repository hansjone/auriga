module github.com/hansjone/auriga/deploy/bootstrap

go 1.27.0

require (
	github.com/hansjone/auriga/infra/gen v0.0.0
	github.com/nats-io/nkeys v0.4.15
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	golang.org/x/sys v0.48.0
)

require (
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	gopkg.in/validator.v2 v2.0.1 // indirect
)

replace github.com/hansjone/auriga/infra/gen => ../../infra/gen
