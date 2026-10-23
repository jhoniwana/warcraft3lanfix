// Package gateway implements the host side gateway.
//
// The Warcraft III client only learns the name of a hosted game from the
// 0x30 answer, and it refreshes its LAN list with the 0x31 announcements the
// host broadcasts to 255.255.255.255. Those broadcasts never cross a
// Tailscale network or a router, so a client that only talks to the host
// over a unicast address would see the game appear and then possibly lose
// it again.
//
// The gateway runs next to the game on the host machine. It polls the local
// game, keeps the game information fresh and serves it to the remote clients
// that ask for it. It also sends the announcements to those clients
// directly, which is exactly what the broadcast would have done on a local
// network.
//
// The gateway does not proxy the game session: the client connects straight
// to the TCP port that the game advertises in the 0x30 packet.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"wc3lan/internal/protocol"
)

// Config configures a gateway.
type Config struct {
	// GameAddr is the address of the local game, "127.0.0.1:6112".
	GameAddr string
	// ListenAddr is the address the gateway answers the remote clients on,
	// "0.0.0.0:6122" by default. The patched client has to use the same
	// port.
	ListenAddr string
	// PollInterval is the delay between two queries to the game.
	PollInterval time.Duration
	// AnnounceInterval is the delay between two announcements sent to the
	// clients.
	AnnounceInterval time.Duration
	// Version is the game version reported in the announcements.
	Version uint32
	// Logger receives the progress messages.
	Logger *log.Logger
}

// Gateway serves the local game to remote clients.
type Gateway struct {
	cfg Config
	log *log.Logger

	udp *net.UDPConn

	mu       sync.Mutex
	game     *protocol.GameInfo
	gamePkt  []byte
	clients  map[string]*net.UDPAddr
	lastSeen time.Time
}

// New validates the configuration and prepares a gateway.
func New(cfg Config) (*Gateway, error) {
	if cfg.GameAddr == "" {
		return nil, errors.New("gateway: the game address is required")
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "0.0.0.0:6122"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.AnnounceInterval <= 0 {
		cfg.AnnounceInterval = 2 * time.Second
	}
	if cfg.Version == 0 {
		cfg.Version = 27
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	return &Gateway{cfg: cfg, log: cfg.Logger, clients: map[string]*net.UDPAddr{}}, nil
}

// Run starts the gateway and blocks until the context is cancelled.
func (g *Gateway) Run(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp4", g.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("gateway: cannot open %s: %w", g.cfg.ListenAddr, err)
	}
	g.udp = conn
	defer g.udp.Close()

	// Closing the socket is what unblocks the readers when the context is
	// cancelled.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	g.log.Printf("gateway ready: game=%s listen=%s", g.cfg.GameAddr, g.cfg.ListenAddr)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); g.serve(ctx) }()
	go func() { defer wg.Done(); g.pollLoop(ctx) }()
	go func() { defer wg.Done(); g.Announce(ctx) }()

	<-ctx.Done()
	wg.Wait()
	return nil
}

// serve answers the queries of the remote clients.
func (g *Gateway) serve(ctx context.Context) {
	buf := make([]byte, 2048)
	for {
		n, from, err := g.udp.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		pkt := buf[:n]
		if len(pkt) < 2 || pkt[1] != protocol.OpQueryForLanGame {
			continue
		}
		g.remember(from)
		g.answer(from)
	}
}

// remember stores the address of a client.
func (g *Gateway) remember(addr *net.UDPAddr) {
	g.mu.Lock()
	defer g.mu.Unlock()
	key := addr.String()
	if _, ok := g.clients[key]; !ok {
		g.log.Printf("client discovered: %s", addr)
	}
	g.clients[key] = addr
}

// answer sends the cached game information to one client.
func (g *Gateway) answer(to *net.UDPAddr) {
	g.mu.Lock()
	pkt := g.gamePkt
	g.mu.Unlock()
	if pkt == nil {
		// The game information is not known yet, ask for it right away.
		g.queryGame()
		g.mu.Lock()
		pkt = g.gamePkt
		g.mu.Unlock()
	}
	if pkt == nil {
		return
	}
	if _, err := g.udp.WriteToUDP(pkt, to); err != nil {
		g.log.Printf("answer to %s failed: %v", to, err)
	}
}

// pollLoop keeps the game information fresh and announces the game.
func (g *Gateway) pollLoop(ctx context.Context) {
	g.queryGame()
	ticker := time.NewTicker(g.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.queryGame()
		}
	}
}

// queryGame asks the local game for its information.
func (g *Gateway) queryGame() {
	addr, err := net.ResolveUDPAddr("udp4", g.cfg.GameAddr)
	if err != nil {
		g.log.Printf("gateway: %v", err)
		return
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		g.log.Printf("gateway: cannot reach the game: %v", err)
		return
	}
	defer conn.Close()

	query, err := protocol.BuildQuery(protocol.GameTypeTFT, 27)
	if err != nil {
		g.log.Printf("gateway: %v", err)
		return
	}
	if _, err := conn.Write(query); err != nil {
		g.log.Printf("gateway: query failed: %v", err)
		return
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		pkt := buf[:n]
		if len(pkt) < 4 || pkt[1] != protocol.OpGameInfo {
			continue
		}
		info, err := protocol.ParseGameInfo(pkt)
		if err != nil {
			g.log.Printf("gateway: cannot parse the game information: %v", err)
			return
		}
		g.mu.Lock()
		changed := g.game == nil || g.game.GameID != info.GameID || g.game.DisplayPlayers() != info.DisplayPlayers()
		g.game = info
		g.gamePkt = append([]byte(nil), pkt...)
		g.lastSeen = time.Now()
		g.mu.Unlock()
		if changed {
			g.log.Printf("game: %q map=%q players=%d/%d port=%d",
				info.Name, info.MapName, info.DisplayPlayers(), info.Slots, info.Port)
		}
		return
	}
}

// Announce sends the game announcements to the known clients.
//
// It runs on its own ticker so the clients keep refreshing their LAN list
// exactly like they do on a local network.
func (g *Gateway) Announce(ctx context.Context) {
	ticker := time.NewTicker(g.cfg.AnnounceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.broadcastAnnouncements()
		}
	}
}

// broadcastAnnouncements sends the 0x31 announcement and the 0x32 player
// count to every known client.
func (g *Gateway) broadcastAnnouncements() {
	g.mu.Lock()
	game := g.game
	gamePkt := g.gamePkt
	clients := make([]*net.UDPAddr, 0, len(g.clients))
	for _, c := range g.clients {
		clients = append(clients, c)
	}
	g.mu.Unlock()

	if game == nil {
		return
	}
	announce, err := protocol.BuildGameAnnounce(game.GameType, g.cfg.Version, game.GameID)
	if err != nil {
		g.log.Printf("gateway: %v", err)
		return
	}
	changed := protocol.BuildPlayersChanged(game.GameID, game.DisplayPlayers(), game.Slots)
	for _, c := range clients {
		if _, err := g.udp.WriteToUDP(announce, c); err != nil {
			g.log.Printf("announce to %s failed: %v", c, err)
		}
		if _, err := g.udp.WriteToUDP(changed, c); err != nil {
			g.log.Printf("player count to %s failed: %v", c, err)
		}
	}
	if gamePkt != nil {
		for _, c := range clients {
			if _, err := g.udp.WriteToUDP(gamePkt, c); err != nil {
				g.log.Printf("game information to %s failed: %v", c, err)
			}
		}
	}
}
