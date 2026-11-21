module github.com/nexusmart/core/evals/assistant

go 1.22

replace (
	github.com/nexusmart/core/gen/go => ../../gen/go
	github.com/nexusmart/core/pkg/common => ../../pkg/common
	github.com/nexusmart/core/services/assistant => ../../services/assistant
)

require (
	github.com/nexusmart/core/pkg/common v0.0.0
	github.com/nexusmart/core/services/assistant v0.0.0
)
