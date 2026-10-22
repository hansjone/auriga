module github.com/hansjone/auriga/infra/ai-gateway

go 1.27.0

require (
	github.com/fergusstrange/embedded-postgres v1.34.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/hansjone/auriga/infra/gen v0.0.0
	github.com/lib/pq v1.10.9
	github.com/nats-io/nats-server/v2 v2.12.6
	github.com/nats-io/nats.go v1.49.0
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/klauspost/compress v1.18.7 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace github.com/hansjone/auriga/infra/gen => ../gen
