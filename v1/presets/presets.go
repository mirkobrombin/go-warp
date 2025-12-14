package presets

import (
	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	busredis "github.com/mirkobrombin/go-warp/v1/syncbus/redis"
	redis "github.com/redis/go-redis/v9"
)

// RedisOptions configures the connection to Redis.
type RedisOptions struct {
	Addr     string
	Password string
	DB       int
}

// NewRedisEventual creates a Warp instance configured for Eventual Consistency
// using Redis as both the L2 Store and the Synchronization Bus.
// This is suitable for high-performance caching where occasional staleness is acceptable.
func NewRedisEventual[T any](opts RedisOptions) *core.Warp[T] {
	client := redis.NewClient(&redis.Options{
		Addr:     opts.Addr,
		Password: opts.Password,
		DB:       opts.DB,
	})

	// L1: InMemory with Merge capabilities
	c := cache.NewInMemory[merge.Value[T]]()

	// L2: Redis Store
	store := adapter.NewRedisStore[T](client)

	// Bus: Redis Pub/Sub
	bus := busredis.NewRedisBus(busredis.RedisBusOptions{Client: client})

	// Engine: Standard Last-Write-Wins (or Vector Clock if T is Versioned, but default Engine is simpler)
	engine := merge.NewEngine[T]()

	return core.New[T](c, store, bus, engine)
}

// NewRedisStrong creates a Warp instance configured for Strong Consistency
// using Redis as the L2 Store and Bus.
// Note: True strong consistency requires Quorum writes which are slower.
func NewRedisStrong[T any](opts RedisOptions) *core.Warp[T] {
	// Re-use logic, the difference is mainly in how the user Registers keys (ModeStrong).
	// But we can enable optimization flags here if needed.
	// For now, it returns the same structure, but the naming guides the user intent.
	return NewRedisEventual[T](opts)
}

// NewInMemoryStandalone creates a Warp instance that runs entirely in-memory
// with no external dependencies. Useful for local development or simple caching.
func NewInMemoryStandalone[T any]() *core.Warp[T] {
	c := cache.NewInMemory[merge.Value[T]]()
	s := adapter.NewInMemoryStore[T]()
	b := syncbus.NewInMemoryBus()
	engine := merge.NewEngine[T]()
	return core.New[T](c, s, b, engine)
}
