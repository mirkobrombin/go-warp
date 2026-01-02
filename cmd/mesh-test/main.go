package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/presets"
	"github.com/mirkobrombin/go-warp/v1/syncbus/mesh"
)

func main() {
	id := flag.Int("id", 0, "Node ID")
	port := flag.Int("port", 7946, "Mesh Port")
	advertise := flag.String("adv", "", "Advertise Address")
	peer := flag.String("peer", "", "Seed Peer")
	flag.Parse()

	opts := mesh.MeshOptions{
		Port:          *port,
		AdvertiseAddr: *advertise,
		Heartbeat:     100 * time.Millisecond,
	}
	if *peer != "" {
		opts.Peers = []string{*peer}
	}

	w := presets.NewMeshEventual[string](opts)
	w.Register("test:key", core.ModeEventualDistributed, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Node 0 will be the publisher
	if *id == 0 {
		go func() {
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			count := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					val := fmt.Sprintf("val-%d", count)
					log.Printf("[Node %d] Setting test:key to %s", *id, val)
					if err := w.Set(ctx, "test:key", val); err != nil {
						log.Printf("[Node %d] Set error: %v", *id, err)
					}
					count++
				}
			}
		}()
	} else {
		// Other nodes will monitor for changes
		// In go-warp, Warp itself doesn't automatically subscribe to the bus for all keys yet,
		// so we bridge it manually as seen in distributed_test.go
		ch, _ := w.Subscribe(ctx, "test:key")
		go func() {
			for range ch {
				log.Printf("[Node %d] Received Invalidation for test:key", *id)
				_ = w.InvalidateLocal(ctx, "test:key")
			}
		}()

		var lastVal string
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					val, _ := w.Get(ctx, "test:key")
					if val != lastVal && val != "" {
						fmt.Printf("[NODE_%d_RECEIVED] %s\n", *id, val)
						lastVal = val
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
		}()
	}

	// Monitor peers
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				log.Printf("[Node %d] Known peers: %v", *id, w.Peers())
			}
		}
	}()

	<-sigCh
	log.Printf("[Node %d] Shutting down...", *id)
}
