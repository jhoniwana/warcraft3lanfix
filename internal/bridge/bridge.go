// Package bridge implements the fallback LAN bridge.
//
// It runs on a player machine: it asks the remote host for the hosted game,
// re-publishes that game on the local network so the unmodified Warcraft III
// client shows it in its LAN list, and proxies the game connection.
//
// The bridge is only needed for unpatched game builds. A patched client talks
// to the host directly and does not need the bridge at all.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"wc3lan/internal/netutil"
	"wc3lan/internal/protocol"
)

// Config configures a bridge.
type Config struct {
	// Host is the address of the machine that runs Warcraft III.
	Host string
	// HostUDPPort is the discovery port on the host. 0 means 6112.
	HostUDPPort int
	// HostTCPPort overrides the game TCP port advertised by the host. 0
	// means the port from the game info packet is used.
	HostTCPPort int
	// ProxyPort is the local TCP port the game connects to. 0 means 6113.
	ProxyPort int
	// GameType and Version are sent with the discovery query.
	GameType protocol.GameType
	Version  uint32
	// PollInterval is the delay between two discovery queries.
	PollInterval time.Duration
	// LocalName optionally renames the game in the LAN list.
	LocalName string
	// Broadcasts replaces the default broadcast targets. Every entry is an
	// address, optionally with a port.
	Broadcasts []string
	// Logger receives the progress messages.
	Logger *log.Logger
}

// Bridge publishes a remote game on the local network.
type Bridge struct {
	cfg Config
	log *log.Logger

	udp *net.UDPConn
	ln  net.Listener

	hostAddr *net.UDPAddr

	mu        sync.Mutex
	game      *protocol.GameInfo
	gamePort  uint16
	lastSeen  time.Time
	announced bool
}

// New validates the configuration and prepares a bridge.
func New(cfg Config) (*Bridge, error) {
	if cfg.Host == "" {
		return nil, errors.New("bridge: the host address is required")
	}
	if cfg.HostUDPPort == 0 {
		cfg.HostUDPPort = protocol.DefaultPort
	}
	if cfg.ProxyPort == 0 {
		cfg.ProxyPort = protocol.DefaultPort + 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.GameType == "" {
		cfg.GameType = protocol.GameTypeTFT
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	return &Bridge{cfg: cfg, log: cfg.Logger}, nil
}

// Run starts the bridge and blocks until the context is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	hostAddr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(b.cfg.Host, fmt.Sprint(b.cfg.HostUDPPort)))
	if err != nil {
		return fmt.Errorf("bridge: cannot resolve the host: %w", err)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return fmt.Errorf("bridge: cannot open the discovery socket: %w", err)
	}
	if err := netutil.EnableBroadcast(conn); err != nil {
		conn.Close()
		return fmt.Errorf("bridge: cannot enable broadcast: %w", err)
	}
	b.udp = conn
	defer b.udp.Close()

	// Closing the socket is what unblocks the reader when the context is
	// cancelled.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	// Keep the resolved address for the poll loop.
	b.hostAddr = hostAddr

	ln, err := net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", b.cfg.ProxyPort))
	if err != nil {
		return fmt.Errorf("bridge: cannot open the proxy port %d: %w", b.cfg.ProxyPort, err)
	}
	b.ln = ln
	defer b.ln.Close()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	b.log.Printf("bridge ready: host=%s udp=%d proxy=tcp/%d type=%s version=%d",
		b.cfg.Host, b.cfg.HostUDPPort, b.cfg.ProxyPort, b.cfg.GameType, b.cfg.Version)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); b.readLoop(ctx) }()
	go func() { defer wg.Done(); b.acceptLoop(ctx) }()
	go func() { defer wg.Done(); b.pollLoop(ctx) }()

	<-ctx.Done()
	b.cancelLocalGame()
	wg.Wait()
	return nil
}

// pollLoop sends the discovery query to the host.
func (b *Bridge) pollLoop(ctx context.Context) {
	query, err := protocol.BuildQuery(b.cfg.GameType, b.cfg.Version)
	if err != nil {
		b.log.Printf("bridge: %v", err)
		return
	}
	hostAddr := b.hostAddr
	if hostAddr == nil {
		var err error
		hostAddr, err = net.ResolveUDPAddr("udp4", net.JoinHostPort(b.cfg.Host, fmt.Sprint(b.cfg.HostUDPPort)))
		if err != nil {
			b.log.Printf("bridge: %v", err)
			return
		}
	}

	ticker := time.NewTicker(b.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := b.udp.WriteToUDP(query, hostAddr); err != nil {
				b.log.Printf("bridge: query failed: %v", err)
			}
		}
	}
}

// readLoop handles the answers of the host.
func (b *Bridge) readLoop(ctx context.Context) {
	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, from, err := b.udp.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		pkt := append([]byte(nil), buf[:n]...)
		switch {
		case pkt[1] == protocol.OpGameInfo:
			b.handleGameInfo(pkt, from)
		case pkt[1] == protocol.OpGameAnnounce || pkt[1] == protocol.OpGamePlayersChanged || pkt[1] == protocol.OpGameCancelled:
			b.rebroadcast(pkt)
		}
	}
}

// handleGameInfo republishes the remote game on the local network.
func (b *Bridge) handleGameInfo(pkt []byte, from *net.UDPAddr) {
	info, err := protocol.ParseGameInfo(pkt)
	if err != nil {
		b.log.Printf("bridge: cannot parse the game info: %v", err)
		return
	}

	b.mu.Lock()
	changed := b.game == nil || b.game.GameID != info.GameID
	b.game = info
	b.gamePort = info.Port
	b.lastSeen = time.Now()
	b.mu.Unlock()

	local := append([]byte(nil), pkt...)
	protocol.SetGameInfoPort(local, uint16(b.cfg.ProxyPort))
	if b.cfg.LocalName != "" {
		protocol.SetGameInfoName(local, b.cfg.LocalName)
	}
	b.rebroadcast(local)

	if changed {
		b.log.Printf("game found: %q map=%q players=%d slots=%d host-port=%d",
			info.Name, info.MapName, info.DisplayPlayers(), info.Slots, info.Port)
	}

	// The game list entry is refreshed with an explicit player count packet.
	announce := protocol.BuildPlayersChanged(info.GameID, info.DisplayPlayers(), info.Slots)
	b.rebroadcast(announce)
}

// rebroadcast sends a packet to the local broadcast address and to the
// broadcast address of every local subnet.
func (b *Bridge) rebroadcast(pkt []byte) {
	for _, target := range b.broadcastTargets() {
		addr, err := net.ResolveUDPAddr("udp4", target)
		if err != nil {
			continue
		}
		if _, err := b.udp.WriteToUDP(pkt, addr); err != nil {
			b.log.Printf("bridge: broadcast to %s failed: %v", target, err)
		}
	}
}

// cancelLocalGame removes the game from the local list.
func (b *Bridge) cancelLocalGame() {
	b.mu.Lock()
	game := b.game
	b.mu.Unlock()
	if game == nil {
		return
	}
	b.rebroadcast(protocol.BuildGameCancelled(game.GameID))
}

// acceptLoop accepts the local game connections and proxies them.
func (b *Bridge) acceptLoop(ctx context.Context) {
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go b.handleConn(conn)
	}
}

// handleConn proxies one local connection to the host.
func (b *Bridge) handleConn(local net.Conn) {
	defer local.Close()

	b.mu.Lock()
	port := b.gamePort
	b.mu.Unlock()
	if b.cfg.HostTCPPort != 0 {
		port = uint16(b.cfg.HostTCPPort)
	}
	if port == 0 {
		port = protocol.DefaultPort
	}

	remote, err := net.DialTimeout("tcp4", net.JoinHostPort(b.cfg.Host, fmt.Sprint(port)), 10*time.Second)
	if err != nil {
		b.log.Printf("bridge: cannot reach the host game port: %v", err)
		return
	}
	defer remote.Close()

	b.log.Printf("proxying a game connection to %s:%d", b.cfg.Host, port)
	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, local); done <- struct{}{} }()
	go func() { io.Copy(local, remote); done <- struct{}{} }()
	<-done
}

// broadcastTargets returns the local broadcast addresses.
func (b *Bridge) broadcastTargets() []string {
	if len(b.cfg.Broadcasts) > 0 {
		out := make([]string, 0, len(b.cfg.Broadcasts))
		for _, t := range b.cfg.Broadcasts {
			if _, _, err := net.SplitHostPort(t); err != nil {
				t = net.JoinHostPort(t, fmt.Sprint(protocol.DefaultPort))
			}
			out = append(out, t)
		}
		return out
	}
	targets := []string{"255.255.255.255"}
	ifaces, err := net.Interfaces()
	if err != nil {
		return targets
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			mask := ipnet.Mask
			if len(mask) != 4 {
				continue
			}
			broadcast := []byte{ip[0] | ^mask[0], ip[1] | ^mask[1], ip[2] | ^mask[2], ip[3] | ^mask[3]}
			targets = append(targets, net.IP(broadcast).String())
		}
	}
	return targets
}
