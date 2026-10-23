// Package sim implements a fake Warcraft III host and a fake client.
//
// They make it possible to test the bridge and the patcher without a copy of
// the game: the fake host answers the discovery queries exactly like the real
// game does, and the fake client behaves like the discovery part of the real
// client.
package sim

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"

	"wc3lan/internal/protocol"
)

// HostConfig configures the fake host.
type HostConfig struct {
	// Listen is the address the host binds, for example 127.0.0.1:6112.
	Listen string
	// Name is the name shown in the LAN list.
	Name string
	// MapName is the map of the simulated game.
	MapName string
	// HostName is the user name of the simulated host.
	HostName string
	// GameType and Version are reported in the answer.
	GameType protocol.GameType
	Version  uint32
	// Logger receives the events.
	Logger *log.Logger
}

// Host is a fake Warcraft III host.
type Host struct {
	cfg    HostConfig
	log    *log.Logger
	udp    *net.UDPConn
	tcp    net.Listener
	gameID uint32
}

// NewHost creates a fake host.
func NewHost(cfg HostConfig) (*Host, error) {
	if cfg.Listen == "" {
		cfg.Listen = "127.0.0.1:6112"
	}
	if cfg.Name == "" {
		cfg.Name = "Local Game"
	}
	if cfg.MapName == "" {
		cfg.MapName = "Maps\\(2)\\Map01.w3m"
	}
	if cfg.HostName == "" {
		cfg.HostName = "Host"
	}
	if cfg.GameType == "" {
		cfg.GameType = protocol.GameTypeTFT
	}
	if cfg.Version == 0 {
		cfg.Version = 27
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &Host{cfg: cfg, log: cfg.Logger, gameID: 1}, nil
}

// Run starts the fake host.
func (h *Host) Run(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp4", h.cfg.Listen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return err
	}
	h.udp = conn
	defer h.udp.Close()

	ln, err := net.Listen("tcp4", h.cfg.Listen)
	if err != nil {
		return err
	}
	h.tcp = ln
	defer h.tcp.Close()

	h.log.Printf("fake host listening on %s", h.cfg.Listen)

	go h.acceptLoop(ctx)
	go h.discoveryLoop(ctx)

	<-ctx.Done()
	return nil
}

// discoveryLoop answers the discovery queries.
func (h *Host) discoveryLoop(ctx context.Context) {
	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, from, err := h.udp.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n < 4 || buf[1] != protocol.OpQueryForLanGame {
			continue
		}
		reply := h.gameInfo()
		if _, err := h.udp.WriteToUDP(reply, from); err != nil {
			h.log.Printf("answer failed: %v", err)
			continue
		}
		h.log.Printf("answered a query from %s", from)
	}
}

// acceptLoop accepts the game connections.
func (h *Host) acceptLoop(ctx context.Context) {
	for {
		conn, err := h.tcp.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			h.log.Printf("game connection from %s", c.RemoteAddr())
			buf := make([]byte, 4096)
			for {
				c.SetReadDeadline(time.Now().Add(2 * time.Minute))
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				// Echo the packet back with a keep alive opcode.
				if n >= 4 && buf[0] == protocol.MagicByte {
					out := make([]byte, 8)
					out[0] = protocol.MagicByte
					out[1] = 0x01
					binary.LittleEndian.PutUint16(out[2:4], 8)
					if _, err := c.Write(out); err != nil {
						return
					}
				}
			}
		}(conn)
	}
}

// gameInfo builds the 0x30 answer.
func (h *Host) gameInfo() []byte {
	settings := make([]byte, 0, 64)
	settings = binary.LittleEndian.AppendUint32(settings, 0)
	settings = append(settings, 0)
	settings = binary.LittleEndian.AppendUint16(settings, 64)
	settings = binary.LittleEndian.AppendUint16(settings, 64)
	settings = binary.LittleEndian.AppendUint32(settings, 0x1234ABCD)
	settings = append(settings, h.cfg.MapName...)
	settings = append(settings, 0)
	settings = append(settings, h.cfg.HostName...)
	settings = append(settings, 0)
	settings = append(settings, 0)
	settings = append(settings, make([]byte, 20)...)
	blob := protocol.EncodeEncodedString(settings)

	p := make([]byte, 0, 128)
	p = append(p, protocol.MagicByte, protocol.OpGameInfo, 0, 0)
	switch h.cfg.GameType {
	case protocol.GameTypeROC:
		p = append(p, '3', 'R', 'A', 'W')
	default:
		p = append(p, 'P', 'X', '3', 'W')
	}
	p = binary.LittleEndian.AppendUint32(p, h.gameID)
	p = binary.LittleEndian.AppendUint32(p, uint32(time.Now().Unix()))
	p = binary.LittleEndian.AppendUint32(p, 0)
	p = append(p, h.cfg.Name...)
	p = append(p, 0)
	p = append(p, blob...)
	p = binary.LittleEndian.AppendUint32(p, 12) // slots
	p = binary.LittleEndian.AppendUint32(p, 0x09)
	p = binary.LittleEndian.AppendUint32(p, 2) // players
	p = binary.LittleEndian.AppendUint32(p, 4) // player slots
	p = binary.LittleEndian.AppendUint32(p, 0)
	p = binary.LittleEndian.AppendUint16(p, uint16(portOf(h.cfg.Listen)))
	binary.LittleEndian.PutUint16(p[2:4], uint16(len(p)))
	return p
}

// portOf extracts the port of an address.
func portOf(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return protocol.DefaultPort
	}
	var port int
	fmt.Sscanf(p, "%d", &port)
	return port
}

// ClientConfig configures the fake client.
type ClientConfig struct {
	// Target is where the query is sent, for example 255.255.255.255:6112.
	Target string
	// Bind is the local address the client listens on. The real game binds
	// the discovery port, so the default is 0.0.0.0:6112.
	Bind string
	// Logger receives the events.
	Logger *log.Logger
}

// Client is a fake Warcraft III client.
type Client struct {
	cfg ClientConfig
	log *log.Logger
	udp *net.UDPConn
}

// NewClient creates a fake client.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.Target == "" {
		cfg.Target = "255.255.255.255:6112"
	}
	if cfg.Bind == "" {
		cfg.Bind = "0.0.0.0:6112"
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	addr, err := net.ResolveUDPAddr("udp4", cfg.Bind)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, log: cfg.Logger, udp: conn}, nil
}

// Query sends a discovery query.
func (c *Client) Query() error {
	addr, err := net.ResolveUDPAddr("udp4", c.cfg.Target)
	if err != nil {
		return err
	}
	query, err := protocol.BuildQuery(protocol.GameTypeTFT, 27)
	if err != nil {
		return err
	}
	if _, err := c.udp.WriteToUDP(query, addr); err != nil {
		return err
	}
	c.log.Printf("query sent to %s", addr)
	return nil
}

// Listen prints the answers that arrive during the given time.
func (c *Client) Listen(d time.Duration) error {
	deadline := time.Now().Add(d)
	c.udp.SetReadDeadline(deadline)
	buf := make([]byte, 4096)
	for {
		n, err := c.udp.Read(buf)
		if err != nil {
			return nil
		}
		pkt := buf[:n]
		if len(pkt) < 4 {
			continue
		}
		switch pkt[1] {
		case protocol.OpGameInfo:
			info, err := protocol.ParseGameInfo(pkt)
			if err != nil {
				c.log.Printf("answer cannot be parsed: %v", err)
				continue
			}
			c.log.Printf("game: %q map=%q players=%d port=%d",
				info.Name, info.MapName, info.DisplayPlayers(), info.Port)
		case protocol.OpGamePlayersChanged:
			c.log.Printf("player count changed")
		case protocol.OpGameCancelled:
			c.log.Printf("game cancelled")
		}
	}
}
