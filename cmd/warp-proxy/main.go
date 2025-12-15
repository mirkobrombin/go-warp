package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tidwall/redcon"
)

var (
	port        = flag.Int("port", 6380, "Port to listen on")
	metricsPort = flag.Int("metrics-port", 2112, "Port for Prometheus metrics")
	addr        = flag.String("addr", "0.0.0.0", "Address to listen on")
)

var (
	opsProcessed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "warp_proxy_ops_total",
		Help: "The total number of processed events",
	})
	activeConns = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "warp_proxy_active_connections",
		Help: "The number of active connections",
	})
)

type server struct {
	warp *core.Warp[[]byte]
}

func main() {
	flag.Parse()

	c := cache.NewInMemory[merge.Value[[]byte]](
		cache.WithMaxEntries[merge.Value[[]byte]](10000),
	)
	bus := syncbus.NewInMemoryBus()
	w := core.New[[]byte](c, nil, bus, nil)

	srv := &server{warp: w}

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		log.Printf("metrics listening on :%d", *metricsPort)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", *metricsPort), mux); err != nil {
			log.Printf("metrics server error: %v", err)
		}
	}()

	listenAddr := fmt.Sprintf("%s:%d", *addr, *port)
	log.Printf("warp-proxy listening on %s (redcon)", listenAddr)

	err := redcon.ListenAndServe(listenAddr,
		srv.handler,
		func(conn redcon.Conn) bool {
			// Accept
			activeConns.Inc()
			return true
		},
		func(conn redcon.Conn, err error) {
			// Closed
			activeConns.Dec()
		},
	)
	if err != nil {
		log.Fatal(err)
	}
}

func (s *server) handler(conn redcon.Conn, cmd redcon.Command) {
	if len(cmd.Args) == 0 {
		return
	}
	opsProcessed.Inc()

	switch strings.ToUpper(string(cmd.Args[0])) {
	case "GET":
		if len(cmd.Args) != 2 {
			conn.WriteError("ERR wrong number of arguments for 'get' command")
			return
		}
		key := string(cmd.Args[1])
		val, err := s.warp.Get(context.Background(), key)
		if err != nil {
			if err == core.ErrNotFound {
				conn.WriteNull()
			} else if err == core.ErrUnregistered {
				// Auto-register on missed access (Sidecar behavior)
				if s.warp.Register(key, core.ModeStrongLocal, 5*time.Minute) {
					// Retry get after registration
					val, err = s.warp.Get(context.Background(), key)
					if err == nil {
						conn.WriteBulk(val)
					} else {
						conn.WriteNull()
					}
				} else {
					conn.WriteError(err.Error())
				}
			} else {
				conn.WriteError(err.Error())
			}
		} else {
			conn.WriteBulk(val)
		}

	case "SET":
		if len(cmd.Args) != 3 {
			conn.WriteError("ERR wrong number of arguments for 'set' command")
			return
		}
		key := string(cmd.Args[1])
		val := cmd.Args[2]

		// Check registration
		_, err := s.warp.Get(context.Background(), key)
		if err == core.ErrUnregistered {
			s.warp.Register(key, core.ModeStrongLocal, 5*time.Minute)
		}

		err = s.warp.Set(context.Background(), key, val)
		if err != nil {
			conn.WriteError(err.Error())
		} else {
			conn.WriteString("OK")
		}

	case "PING":
		if len(cmd.Args) > 1 {
			conn.WriteBulk(cmd.Args[1])
		} else {
			conn.WriteString("PONG")
		}

	case "INFO":
		conn.WriteBulkString("# Server\r\nredis_version:6.0.0\r\nwarp_version:1.0.0\r\n")

	case "COMMAND", "CLIENT", "AUTH", "SELECT":
		// Admin/Connection commands as No-Op to satisfy client libraries
		conn.WriteString("OK")

	case "QUIT":
		conn.WriteString("OK")
		conn.Close()

	default:
		conn.WriteError("ERR unknown command '" + string(cmd.Args[0]) + "'")
	}
}
