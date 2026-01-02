package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mirkobrombin/go-warp/v1/adapter"
	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/presets"
	"github.com/mirkobrombin/go-warp/v1/syncbus/mesh"
	redis "github.com/redis/go-redis/v9"
)

func main() {
	port := flag.Int("port", 8080, "HTTP port")
	meshPort := flag.Int("mesh-port", 7946, "Mesh port")
	peers := flag.String("peers", "", "Comma-separated list of peers (e.g. 127.0.0.1:7947)")
	mode := flag.String("mode", "mesh", "mesh or redis")
	redisAddr := flag.String("redis-addr", "localhost:6379", "Redis address")
	flag.Parse()

	var w *core.Warp[string]

	if *mode == "mesh" {
		mOpts := mesh.MeshOptions{
			Port:          *meshPort,
			AdvertiseAddr: fmt.Sprintf("127.0.0.1:%d", *meshPort),
		}
		if *peers != "" {
			mOpts.Peers = strings.Split(*peers, ",")
		}

		bus, _ := mesh.NewMeshBus(mOpts)
		c := cache.NewInMemory[merge.Value[string]]()

		// Use Redis as shared L2 store even for Mesh bus test
		rClient := redis.NewClient(&redis.Options{Addr: *redisAddr})
		store := adapter.NewRedisStore[string](rClient)

		engine := merge.NewEngine[string]()
		w = core.New[string](c, store, bus, engine)
	} else {
		w = presets.NewRedisEventual[string](presets.RedisOptions{
			Addr: *redisAddr,
		})
	}

	// Register a test key
	w.Register("smoke-key", core.ModeEventualDistributed, time.Hour)

	http.HandleFunc("/set", func(wWriter http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		val := r.URL.Query().Get("val")
		if err := w.Set(r.Context(), key, val); err != nil {
			http.Error(wWriter, err.Error(), 500)
			return
		}
		fmt.Fprintf(wWriter, "OK")
	})

	http.HandleFunc("/get", func(wWriter http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		val, err := w.Get(r.Context(), key)
		if err != nil {
			http.Error(wWriter, "warp: not found", 404)
			return
		}
		fmt.Fprintf(wWriter, "%s", val)
	})

	log.Printf("Smoke test node listening on :%d (mode: %s)...", *port, *mode)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}
