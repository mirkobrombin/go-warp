package mesh

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/mirkobrombin/go-warp/v1/syncbus"
	"golang.org/x/net/ipv4"
)

// MeshOptions configures the Warp Mesh bus.
type MeshOptions struct {
	Port          int
	Interface     string
	Group         string
	Peers         []string      // Static seeds for unicast gossip
	AdvertiseAddr string        // Address to advertise to other peers (e.g. "10.0.0.1:7946")
	Heartbeat     time.Duration // Interval for heartbeat gossip (default 5s)
	BatchInterval time.Duration // Max time to wait before flushing a batch (default 100ms)
	BatchSize     int           // Max number of keys in a batch (default 10)
}

// MeshBus implements a peer-to-peer synchronization bus using UDP Multicast and Unicast Gossip.
type MeshBus struct {
	opts      MeshOptions
	nodeID    [16]byte
	conn      net.PacketConn
	pconn     *ipv4.PacketConn
	groupAddr *net.UDPAddr

	mu   sync.RWMutex
	subs map[string][]chan syncbus.Event

	peersMu      sync.RWMutex
	knownPeers   map[string]time.Time
	resolvedAddr map[string]*net.UDPAddr

	publishCh chan string
	pending   map[string]struct{}

	published atomic.Uint64
	received  atomic.Uint64

	ctx    context.Context
	cancel context.CancelFunc
}

// NewMeshBus creates a new Warp Mesh synchronization bus.
func NewMeshBus(opts MeshOptions) (*MeshBus, error) {
	if opts.Port == 0 {
		opts.Port = 7946
	}
	if opts.Group == "" {
		opts.Group = "239.0.0.1"
	}

	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", opts.Group, opts.Port))
	if err != nil {
		return nil, fmt.Errorf("mesh: failed to resolve multicast address: %w", err)
	}

	// Configure socket to allow multiple listeners on the same port.
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			c.Control(func(fd uintptr) {
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
				_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, 15, 1)
			})
			return err
		},
	}

	c, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", opts.Port))
	if err != nil {
		return nil, fmt.Errorf("mesh: failed to listen on port %d: %w", opts.Port, err)
	}

	pconn := ipv4.NewPacketConn(c)

	var iface *net.Interface
	if opts.Interface != "" {
		iface, err = net.InterfaceByName(opts.Interface)
		if err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("mesh: failed to find interface %s: %w", opts.Interface, err)
		}
	}

	if err := pconn.JoinGroup(iface, addr); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mesh: failed to join group %s: %w", opts.Group, err)
	}

	if iface != nil {
		if err := pconn.SetMulticastInterface(iface); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("mesh: failed to set multicast interface: %w", err)
		}
	}

	// Enable loopback so multiple nodes on same host can hear each other.
	_ = pconn.SetMulticastLoopback(true)

	if opts.BatchInterval == 0 {
		opts.BatchInterval = 100 * time.Millisecond
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = 20
	}

	ctx, cancel := context.WithCancel(context.Background())
	b := &MeshBus{
		opts:         opts,
		nodeID:       uuid.New(),
		conn:         c,
		pconn:        pconn,
		groupAddr:    addr,
		subs:         make(map[string][]chan syncbus.Event),
		knownPeers:   make(map[string]time.Time),
		resolvedAddr: make(map[string]*net.UDPAddr),
		publishCh:    make(chan string, 1000),
		pending:      make(map[string]struct{}),
		ctx:          ctx,
		cancel:       cancel,
	}

	go b.listen()
	go b.heartbeatLoop()
	go b.cleanupPeers()
	go b.runBatcher()

	return b, nil
}

// Publish sends an invalidation event to the mesh. It uses internal batching.
func (b *MeshBus) Publish(ctx context.Context, key string, opts ...syncbus.PublishOption) error {
	b.mu.Lock()
	if _, ok := b.pending[key]; ok {
		b.mu.Unlock()
		return nil // Already pending delivery
	}
	b.pending[key] = struct{}{}
	b.mu.Unlock()

	select {
	case b.publishCh <- key:
		return nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, key)
		b.mu.Unlock()
		return ctx.Err()
	}
}

// broadcast sends the packet payload to multicast group and known unicast peers.
func (b *MeshBus) broadcast(payload []byte) error {
	// Multicast is the primary delivery mechanism
	_, err := b.conn.WriteTo(payload, b.groupAddr)
	if err == nil {
		b.published.Add(1)
	}

	// Unicast to known peers (backup/cloud traversal)
	b.peersMu.RLock()
	// Copy pointers to avoid holding the lock during WriteTo
	addrs := make([]*net.UDPAddr, 0, len(b.resolvedAddr)+len(b.opts.Peers))
	for _, addr := range b.resolvedAddr {
		addrs = append(addrs, addr)
	}
	b.peersMu.RUnlock()

	// Send to resolved peers
	for _, addr := range addrs {
		_, _ = b.conn.WriteTo(payload, addr)
	}

	// Send to seed peers too if not yet resolved/known
	for _, peer := range b.opts.Peers {
		b.peersMu.RLock()
		_, known := b.resolvedAddr[peer]
		b.peersMu.RUnlock()
		if known {
			continue
		}

		addr, err := net.ResolveUDPAddr("udp4", peer)
		if err != nil {
			continue
		}
		_, _ = b.conn.WriteTo(payload, addr)
	}

	return err
}

// PublishAndAwait is not fully supported by UDP Mesh due to its fire-and-forget nature.
func (b *MeshBus) PublishAndAwait(ctx context.Context, key string, replicas int, opts ...syncbus.PublishOption) error {
	if replicas > 0 {
		return syncbus.ErrQuorumUnsupported
	}
	return b.Publish(ctx, key, opts...)
}

// PublishAndAwaitTopology is not supported by UDP Mesh.
func (b *MeshBus) PublishAndAwaitTopology(ctx context.Context, key string, minZones int, opts ...syncbus.PublishOption) error {
	return syncbus.ErrQuorumUnsupported
}

// Subscribe registers a channel to receive invalidation events for a key.
func (b *MeshBus) Subscribe(ctx context.Context, key string) (<-chan syncbus.Event, error) {
	ch := make(chan syncbus.Event, 32)
	b.mu.Lock()
	b.subs[key] = append(b.subs[key], ch)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = b.Unsubscribe(context.Background(), key, ch)
	}()

	return ch, nil
}

// Unsubscribe removes a channel from key subscriptions.
func (b *MeshBus) Unsubscribe(ctx context.Context, key string, ch <-chan syncbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	subs, ok := b.subs[key]
	if !ok {
		return nil
	}

	for i, c := range subs {
		if c == ch {
			b.subs[key] = append(subs[:i], subs[i+1:]...)
			close(chan syncbus.Event(c))
			break
		}
	}

	if len(b.subs[key]) == 0 {
		delete(b.subs, key)
	}

	return nil
}

// IsHealthy returns true if the underlying UDP connection is active.
func (b *MeshBus) IsHealthy() bool {
	return b.conn != nil
}

// RevokeLease is a convenience for Publish with a lease prefix.
func (b *MeshBus) RevokeLease(ctx context.Context, id string) error {
	return b.Publish(ctx, "lease:"+id)
}

// SubscribeLease is a convenience for Subscribe with a lease prefix.
func (b *MeshBus) SubscribeLease(ctx context.Context, id string) (<-chan syncbus.Event, error) {
	return b.Subscribe(ctx, "lease:"+id)
}

// UnsubscribeLease is a convenience for Unsubscribe with a lease prefix.
func (b *MeshBus) UnsubscribeLease(ctx context.Context, id string, ch <-chan syncbus.Event) error {
	return b.Unsubscribe(ctx, "lease:"+id, ch)
}

func (b *MeshBus) listen() {
	buf := make([]byte, 1500)
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}

		n, _, err := b.conn.ReadFrom(buf)
		if err != nil {
			continue
		}

		var p packet
		if err := p.unmarshal(buf[:n]); err != nil {
			continue
		}

		if p.NodeID == b.nodeID {
			continue
		}

		b.received.Add(1)

		if p.Type == typeHeartbeat {
			b.peersMu.Lock()
			addrStr := string(p.Key)
			b.knownPeers[addrStr] = time.Now()
			if _, ok := b.resolvedAddr[addrStr]; !ok {
				if rAddr, err := net.ResolveUDPAddr("udp4", addrStr); err == nil {
					b.resolvedAddr[addrStr] = rAddr
				}
			}
			b.peersMu.Unlock()
			continue
		}

		if p.Type == typeInvalidate {
			b.handleInvalidate(string(p.Key))
		}

		if p.Type == typeBatch {
			for _, key := range p.Keys {
				b.handleInvalidate(key)
			}
		}
	}
}

func (b *MeshBus) handleInvalidate(key string) {
	b.mu.RLock()
	chans, ok := b.subs[key]
	if ok {
		evt := syncbus.Event{Key: key}
		for _, ch := range chans {
			select {
			case ch <- evt:
			default:
			}
		}
	}
	b.mu.RUnlock()
}

// Close gracefully shuts down the mesh bus.
func (b *MeshBus) Close() error {
	b.cancel()
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

func (b *MeshBus) heartbeatLoop() {
	interval := b.opts.Heartbeat
	if interval == 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			addr := b.opts.AdvertiseAddr
			if addr == "" {
				// Fallback to local address if possible, though AdvertiseAddr is preferred
				addr = b.conn.LocalAddr().String()
			}

			p := packet{
				Magic:  magicByte,
				Type:   typeHeartbeat,
				NodeID: b.nodeID,
				KeyLen: uint16(len(addr)),
				Key:    []byte(addr),
			}

			buf := bufferPool.Get().([]byte)
			n, err := p.marshal(buf)
			if err == nil {
				_ = b.broadcast(buf[:n])
			}
			bufferPool.Put(buf)
		}
	}
}

func (b *MeshBus) cleanupPeers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.peersMu.Lock()
			now := time.Now()
			for addr, lastSeen := range b.knownPeers {
				if now.Sub(lastSeen) > 60*time.Second {
					delete(b.knownPeers, addr)
					delete(b.resolvedAddr, addr)
				}
			}
			b.peersMu.Unlock()
		}
	}
}

func (b *MeshBus) runBatcher() {
	ticker := time.NewTicker(b.opts.BatchInterval)
	defer ticker.Stop()

	var batch []string

	flush := func() {
		if len(batch) == 0 {
			return
		}

		p := packet{
			Magic:  magicByte,
			Type:   typeBatch,
			NodeID: b.nodeID,
			Keys:   batch,
		}

		buf := bufferPool.Get().([]byte)
		if n, err := p.marshal(buf); err == nil {
			_ = b.broadcast(buf[:n])
		}
		bufferPool.Put(buf)

		// Cleanup pending map
		b.mu.Lock()
		for _, k := range batch {
			delete(b.pending, k)
		}
		b.mu.Unlock()
		batch = nil
	}

	for {
		select {
		case <-b.ctx.Done():
			return
		case key := <-b.publishCh:
			batch = append(batch, key)
			if len(batch) >= b.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Metrics returns the published and received counts.
func (b *MeshBus) Metrics() syncbus.Metrics {
	return syncbus.Metrics{
		Published: b.published.Load(),
		Delivered: b.received.Load(),
	}
}

// Peers returns a list of currently known active peers.
func (b *MeshBus) Peers() []string {
	b.peersMu.RLock()
	defer b.peersMu.RUnlock()

	peers := make([]string, 0, len(b.knownPeers))
	for addr := range b.knownPeers {
		peers = append(peers, addr)
	}
	return peers
}
