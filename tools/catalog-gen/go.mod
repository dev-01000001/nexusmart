module github.com/nexusmart/core/tools/catalog-gen

go 1.22

replace (
	github.com/nexusmart/core/gen/go => ../../gen/go
	github.com/nexusmart/core/pkg/common => ../../pkg/common
)

require (
	github.com/nexusmart/core/gen/go v0.0.0
	github.com/nexusmart/core/pkg/common v0.0.0
	google.golang.org/grpc v1.65.0
)

require (
	github.com/golang-jwt/jwt/v5 v5.2.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240701130421-f6361c86f094 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)
