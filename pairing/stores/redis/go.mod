module github.com/alva-ai/tg-managed-bot-go/pairing/stores/redis

go 1.25.7

replace github.com/alva-ai/tg-managed-bot-go => ../../..

require (
	github.com/alicebob/miniredis/v2 v2.37.0
	github.com/alva-ai/tg-managed-bot-go v0.0.0-00010101000000-000000000000
	github.com/redis/go-redis/v9 v9.18.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)
