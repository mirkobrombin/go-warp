package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/mirkobrombin/go-warp/v1/core"
	"github.com/mirkobrombin/go-warp/v1/merge"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
)

var (
	port = flag.Int("port", 6380, "Port to listen on")
	addr = flag.String("addr", "0.0.0.0", "Address to listen on")
)

type server struct {
	warp *core.Warp[[]byte]
}

func main() {
	flag.Parse()

	// Initialize Warp
	// We use InMemory options for the sidecar for simplicity in V1,
	// assuming it acts as a standalone cache or connected via external bus if configured.
	// For V2 we would parse config to Connect to Redis/Bus.
	c := cache.NewInMemory[merge.Value[[]byte]](
		cache.WithMaxEntries[merge.Value[[]byte]](10000),
	)
	bus := syncbus.NewInMemoryBus()
	// No store for pure caching proxy sidecar by default
	w := core.New[[]byte](c, nil, bus, nil)

	srv := &server{warp: w}

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", *addr, *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	log.Printf("warp-proxy listening on %s:%d", *addr, *port)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("failed to accept: %v", err)
			continue
		}
		go srv.handle(conn)
	}
}

func (s *server) handle(conn net.Conn) {
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	respReader := NewRESPReader(reader)
	respWriter := NewRESPWriter(writer)

	for {
		// Read first command
		args, err := respReader.ReadCommand()
		if err != nil {
			if err != io.EOF {
				log.Printf("read error: %v", err)
			}
			return
		}

		s.execute(respWriter, args)

		// Consume pipelined commands from buffer
		for reader.Buffered() > 0 {
			args, err := respReader.ReadCommand()
			if err != nil {
				respWriter.Flush()
				return
			}
			s.execute(respWriter, args)
		}

		if err := respWriter.Flush(); err != nil {
			return
		}
	}
}

func (s *server) execute(w *RESPWriter, args [][]byte) {
	if len(args) == 0 {
		return
	}

	cmd := string(args[0])
	if len(cmd) > 0 {
		cmd = strings.ToUpper(cmd)
	}

	switch cmd {
	case "GET":
		if len(args) < 2 {
			w.WriteError("ERR wrong number of arguments for 'get' command")
			return
		}
		key := string(args[1])

		val, err := s.warp.Get(context.Background(), key)
		if err != nil {
			if err == core.ErrNotFound {
				w.WriteNull()
			} else if err == core.ErrUnregistered {
				if s.warp.Register(key, core.ModeStrongLocal, 5*time.Minute) {
					val, err = s.warp.Get(context.Background(), key)
					if err == nil {
						w.WriteBulk(val)
					} else {
						w.WriteNull()
					}
				} else {
					w.WriteError(err.Error())
				}
			} else {
				w.WriteError(err.Error())
			}
		} else {
			w.WriteBulk(val)
		}
	case "SET":
		if len(args) < 3 {
			w.WriteError("ERR wrong number of arguments for 'set' command")
			return
		}
		key := string(args[1])
		val := args[2] // Value is []byte, good!

		// Check reg logic
		_, err := s.warp.Get(context.Background(), key)
		if err == core.ErrUnregistered {
			s.warp.Register(key, core.ModeStrongLocal, 5*time.Minute)
		}

		err = s.warp.Set(context.Background(), key, val)
		if err != nil {
			w.WriteError(err.Error())
		} else {
			w.WriteSimpleString("OK")
		}
	case "PING":
		if len(args) > 1 {
			w.WriteBulk(args[1])
		} else {
			w.WriteSimpleString("PONG")
		}
	case "COMMAND":
		w.WriteSimpleString("OK")
	case "INFO":
		w.WriteBulk([]byte("# Server\r\nredis_version:6.0.0\r\nwarp_version:1.0.0\r\n"))
	case "CLIENT":
		w.WriteSimpleString("OK")
	default:
		w.WriteError(fmt.Sprintf("ERR unknown command '%s'", cmd))
	}
}

// Methods below are replaced by RESPWriter methods
