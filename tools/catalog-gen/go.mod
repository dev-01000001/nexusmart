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
	google.golang.org/protobuf v1.34.2
)
