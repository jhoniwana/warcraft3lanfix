// Command wc3lan makes classic Warcraft III LAN games work outside the local
// subnet.
//
// Subcommands:
//
//	probe     ask a host for its game and print the answer
//	bridge    publish a remote game on the local network and proxy it
//	patch     point the client discovery query at a host address
//	unpatch   restore the original executable
//	inspect   show what the patch would change
//	sim-host  fake Warcraft III host, used by the tests
//	sim-client fake Warcraft III client, used by the tests
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wc3lan/internal/bridge"
	"wc3lan/internal/gateway"
	"wc3lan/internal/patch"
	"wc3lan/internal/protocol"
	"wc3lan/internal/sim"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "probe":
		err = cmdProbe(os.Args[2:])
	case "bridge":
		err = cmdBridge(os.Args[2:])
	case "gateway":
		err = cmdGateway(os.Args[2:])
	case "patch":
		err = cmdPatch(os.Args[2:])
	case "unpatch":
		err = cmdUnpatch(os.Args[2:])
	case "inspect":
		err = cmdInspect(os.Args[2:])
	case "sim-host":
		err = cmdSimHost(os.Args[2:])
	case "sim-client":
		err = cmdSimClient(os.Args[2:])
	case "version":
		fmt.Println("wc3lan 0.1.0")
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `wc3lan - classic Warcraft III LAN over Tailscale or the internet

usage:
  wc3lan probe      --host <ip> [--udp-port 6112]
  wc3lan bridge     --host <ip> [--proxy-port 6113] [--type W3XP] [--version 27]
  wc3lan gateway    [--game 127.0.0.1:6112] [--listen 0.0.0.0:6122]
  wc3lan patch      <game.dll> --host <ip> [--dry-run]
  wc3lan unpatch    <game.dll>
  wc3lan inspect    <game.dll>
  wc3lan sim-host   [--listen 127.0.0.1:6112]
  wc3lan sim-client [--broadcast 127.0.0.1:6112] [--count 1]

The 1.27b LAN code lives in game.dll, not in war3.exe.

`)
}

// splitArgs moves the positional arguments to the end so that flags may be
// written before or after the file name.
//
// The flag package stops parsing at the first non flag argument, so
// "patch Game.dll --host 10.0.0.2" would otherwise never see --host.
func splitArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) > 1 && a[0] == '-' {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			if f := fs.Lookup(name); f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					continue
				}
			}
			if i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// ---------------------------------------------------------------- probe ----

func cmdProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	host := fs.String("host", "", "address of the machine that runs Warcraft III")
	udpPort := fs.Int("udp-port", protocol.DefaultPort, "discovery port on the host")
	timeout := fs.Duration("timeout", 3*time.Second, "how long to wait for the answer")
	gameType := fs.String("type", "W3XP", "W3XP (Frozen Throne) or WAR3 (Reign of Chaos)")
	version := fs.Uint("version", 27, "game version sent with the query")
	fs.Parse(args)
	if *host == "" {
		return errors.New("--host is required")
	}
	gt, err := protocol.ParseGameType(*gameType)
	if err != nil {
		return err
	}

	query, err := protocol.BuildQuery(gt, uint32(*version))
	if err != nil {
		return err
	}
	addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(*host, fmt.Sprint(*udpPort)))
	if err != nil {
		return err
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", addr, err)
	}
	defer conn.Close()
	if _, err := conn.Write(query); err != nil {
		return err
	}
	conn.SetReadDeadline(time.Now().Add(*timeout))

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return fmt.Errorf("no answer from %s: %w", addr, err)
		}
		pkt := buf[:n]
		if len(pkt) < 4 || pkt[1] != protocol.OpGameInfo {
			continue
		}
		info, err := protocol.ParseGameInfo(pkt)
		if err != nil {
			return fmt.Errorf("cannot parse the answer: %w", err)
		}
		fmt.Printf("answer from %s\n", conn.RemoteAddr())
		fmt.Printf("  game      : %q (id %d)\n", info.Name, info.GameID)
		fmt.Printf("  type      : %s\n", info.GameType)
		fmt.Printf("  map       : %s (%dx%d)\n", info.MapName, info.MapWidth, info.MapHeight)
		fmt.Printf("  host      : %s\n", info.HostName)
		fmt.Printf("  players   : %d displayed, %d slots\n", info.DisplayPlayers(), info.Slots)
		fmt.Printf("  game port : %d (TCP)\n", info.Port)
		return nil
	}
}

// --------------------------------------------------------------- bridge ----

func cmdBridge(args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ExitOnError)
	host := fs.String("host", "", "address of the machine that runs Warcraft III")
	hostUDPPort := fs.Int("host-udp-port", protocol.DefaultPort, "discovery port on the host")
	hostTCPPort := fs.Int("host-tcp-port", 0, "game TCP port on the host, 0 uses the advertised port")
	proxyPort := fs.Int("proxy-port", protocol.DefaultPort+1, "local TCP port the game connects to")
	gameType := fs.String("type", "W3XP", "W3XP (Frozen Throne) or WAR3 (Reign of Chaos)")
	version := fs.Uint("version", 27, "game version sent with the query")
	name := fs.String("name", "", "optional name shown in the LAN list")
	broadcast := fs.String("broadcast", "", "comma separated broadcast targets, replaces the automatic ones")
	verbose := fs.Bool("verbose", false, "print every event")
	fs.Parse(args)
	if *host == "" {
		return errors.New("--host is required")
	}
	gt, err := protocol.ParseGameType(*gameType)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "", log.Ltime)
	if !*verbose {
		logger = log.New(os.Stderr, "wc3lan: ", log.Ltime)
	}

	var broadcasts []string
	if *broadcast != "" {
		for _, t := range strings.Split(*broadcast, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				broadcasts = append(broadcasts, t)
			}
		}
	}

	b, err := bridge.New(bridge.Config{
		Host:        *host,
		HostUDPPort: *hostUDPPort,
		HostTCPPort: *hostTCPPort,
		ProxyPort:   *proxyPort,
		GameType:    gt,
		Version:     uint32(*version),
		LocalName:   *name,
		Broadcasts:  broadcasts,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return b.Run(ctx)
}

// -------------------------------------------------------------- gateway ----

func cmdGateway(args []string) error {
	fs := flag.NewFlagSet("gateway", flag.ExitOnError)
	game := fs.String("game", "127.0.0.1:6112", "address of the local Warcraft III game")
	listen := fs.String("listen", "0.0.0.0:6122", "address the remote clients connect to")
	version := fs.Uint("version", 27, "game version reported in the announcements")
	poll := fs.Duration("poll", time.Second, "delay between two queries to the game")
	announce := fs.Duration("announce", 2*time.Second, "delay between two announcements")
	fs.Parse(args)

	gw, err := gateway.New(gateway.Config{
		GameAddr:         *game,
		ListenAddr:       *listen,
		Version:          uint32(*version),
		PollInterval:     *poll,
		AnnounceInterval: *announce,
		Logger:           log.New(os.Stderr, "wc3lan: ", log.Ltime),
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return gw.Run(ctx)
}

// ---------------------------------------------------------------- patch ----
func cmdPatch(args []string) error {
	fs := flag.NewFlagSet("patch", flag.ExitOnError)
	host := fs.String("host", "", "address the client must query")
	dryRun := fs.Bool("dry-run", false, "only show what would change")
	specName := fs.String("spec", "", "patch definition to use, default detects the build")
	fs.Parse(splitArgs(fs, args))
	if fs.NArg() < 1 {
		return errors.New("the path of game.dll is required")
	}
	if *host == "" {
		return errors.New("--host is required")
	}
	ip := net.ParseIP(*host)
	if ip == nil {
		return fmt.Errorf("%q is not an IP address", *host)
	}
	spec, err := patch.SelectSpec(fs.Arg(0), *specName)
	if err != nil {
		return err
	}
	rep, err := patch.Apply(fs.Arg(0), ip, spec, *dryRun)
	if err != nil {
		return err
	}
	fmt.Printf("file    : %s\n", rep.Path)
	fmt.Printf("build   : %s\n", rep.Version)
	fmt.Printf("target  : %s\n", *host)
	if rep.BackupPath != "" {
		fmt.Printf("backup  : %s\n", rep.BackupPath)
	}
	fmt.Print(rep.Describe())
	return nil
}

func cmdUnpatch(args []string) error {
	fs := flag.NewFlagSet("unpatch", flag.ExitOnError)
	fs.Parse(splitArgs(fs, args))
	if fs.NArg() < 1 {
		return errors.New("the path of game.dll is required")
	}
	backup, err := patch.Restore(fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("restored %s from %s\n", fs.Arg(0), backup)
	return nil
}

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	specName := fs.String("spec", "", "patch definition to use")
	fs.Parse(splitArgs(fs, args))
	if fs.NArg() < 1 {
		return errors.New("the path of game.dll is required")
	}
	spec, err := patch.SelectSpec(fs.Arg(0), *specName)
	if err != nil {
		return err
	}
	matches, err := patch.Inspect(fs.Arg(0), spec)
	if err != nil {
		return err
	}
	fmt.Printf("build %s\n", spec.Version)
	for _, m := range matches {
		fmt.Printf("  %-28s file+0x%X va=0x%X bytes=%02X%02X%02X%02X\n",
			m.Site, m.FileOffset, m.VA, m.OldBytes[0], m.OldBytes[1], m.OldBytes[2], m.OldBytes[3])
	}
	return nil
}

// ------------------------------------------------------------------ sim ----

func cmdSimHost(args []string) error {
	fs := flag.NewFlagSet("sim-host", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:6112", "address and port to listen on")
	name := fs.String("name", "Local Game", "name of the simulated game")
	mapName := fs.String("map", "Maps\\(2)\\Map01.w3m", "map of the simulated game")
	fs.Parse(args)
	host, err := sim.NewHost(sim.HostConfig{
		Listen:  *listen,
		Name:    *name,
		MapName: *mapName,
		Logger:  log.New(os.Stderr, "sim-host: ", log.Ltime),
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return host.Run(ctx)
}

func cmdSimClient(args []string) error {
	fs := flag.NewFlagSet("sim-client", flag.ExitOnError)
	target := fs.String("broadcast", "255.255.255.255:6112", "where to send the query")
	bind := fs.String("bind", "0.0.0.0:6112", "local address to listen on")
	count := fs.Int("count", 1, "how many queries to send")
	wait := fs.Duration("wait", 2*time.Second, "how long to wait for the answers")
	fs.Parse(args)
	client, err := sim.NewClient(sim.ClientConfig{
		Target: *target,
		Bind:   *bind,
		Logger: log.New(os.Stderr, "sim-client: ", log.Ltime),
	})
	if err != nil {
		return err
	}
	for i := 0; i < *count; i++ {
		if err := client.Query(); err != nil {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return client.Listen(*wait)
}

// versionString is used by the tests to check the reported version.
func versionString(v string) string { return strings.TrimSpace(v) }
