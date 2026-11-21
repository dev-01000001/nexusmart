module github.com/nexusmart/core/services/chat

go 1.22

replace (
	github.com/nexusmart/core/gen/go => ../../gen/go
	github.com/nexusmart/core/pkg/common => ../../pkg/common
)

require (
	github.com/alicebob/miniredis/v2 v2.33.0
	github.com/coder/websocket v1.8.12
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.6.0
	github.com/nexusmart/core/gen/go v0.0.0
	github.com/nexusmart/core/pkg/common v0.0.0
	github.com/prometheus/client_golang v1.19.1
	github.com/redis/go-redis/v9 v9.6.1
	google.golang.org/grpc v1.65.0
	google.golang.org/protobuf v1.34.2
)
