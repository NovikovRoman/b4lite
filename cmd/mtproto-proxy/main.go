// Command mtproto-proxy is a standalone MTProto (Telegram) proxy, extracted
// from the MTProto Proxy feature of https://github.com/DanielLavrushin/b4.
//
// It runs the same fake-TLS proxy server b4 exposes under
// Settings > MTProto Proxy: Telegram clients connect to it directly (no
// packet-mangling / DPI-circumvention engine involved), it hides itself
// behind a fake TLS handshake to a real-looking domain, and relays traffic
// to the appropriate Telegram data center.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/NovikovRoman/b4lite/internal/config"
	"github.com/NovikovRoman/b4lite/internal/log"
	"github.com/NovikovRoman/b4lite/internal/mtproto"
)

var version = "dev"

// repeatableFlag lets -secret / -secret-host be passed more than once, e.g.
//
//	-secret alice=ee0102... -secret bob=ee0304...
//	-secret-host www.cloudflare.com -secret-host www.microsoft.com
type repeatableFlag []string

func (r *repeatableFlag) String() string     { return strings.Join(*r, ",") }
func (r *repeatableFlag) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "gen-secret":
			genSecretCmd(os.Args[2:])
			return
		case "-h", "-help", "--help":
			printUsage()
			return
		}
	}
	runCmd(os.Args[1:])
}

func printUsage() {
	fmt.Println("usage:")
	fmt.Println("  mtproto-proxy [flags]              run the proxy (default)")
	fmt.Println("  mtproto-proxy gen-secret [flags]    generate one or more fake-TLS secrets and exit")
	fmt.Println()
	fmt.Println("run flags:")
	flag.NewFlagSet("run", flag.ExitOnError).PrintDefaults()
}

// ---------------------------------------------------------------------
// run (default command): start the proxy server
// ---------------------------------------------------------------------

func runCmd(args []string) {
	fs := flag.NewFlagSet("mtproto-proxy", flag.ExitOnError)
	var (
		secretFlags repeatableFlag
		hostFlags   repeatableFlag
	)
	var (
		configPath  = fs.String("config", "", "path to a JSON config file (created/updated automatically if it doesn't exist)")
		bind        = fs.String("bind", "", "override bind address (default 0.0.0.0)")
		port        = fs.Int("port", 0, "override listen port (default 3128)")
		fakeSNI     = fs.String("fake-sni", "", "override the fake-TLS masking domain")
		upstream    = fs.String("upstream", "", "upstream mode: auto | ws | tcp (default auto)")
		verbose     = fs.Int("v", 1, "log verbosity: 0=error 1=info 2=trace 3=debug")
		showVersion = fs.Bool("version", false, "print version and exit")
	)
	fs.Var(&secretFlags, "secret", "add an exact hex secret; repeatable. Accepts \"NAME=HEXSECRET\" or just \"HEXSECRET\". Each secret is its own independently revocable Telegram login")
	fs.Var(&hostFlags, "secret-host", "auto-generate a secret for this fronting hostname; repeatable. Ignored if any -secret is given")
	_ = fs.Parse(args)

	if *showVersion {
		fmt.Println("mtproto-proxy", version)
		return
	}

	log.Init(os.Stderr, log.LevelFromVerbose(*verbose), true)

	cfg := config.DefaultConfig
	cfg.ConfigPath = *configPath
	if *configPath != "" {
		if err := cfg.LoadFromFile(*configPath); err != nil {
			log.Errorf("failed to load config %s: %v", *configPath, err)
			os.Exit(1)
		}
		cfg.ConfigPath = *configPath
	}

	// CLI flags override whatever came from the config file / defaults.
	if *bind != "" {
		cfg.System.MTProto.BindAddress = *bind
	}
	if *port != 0 {
		cfg.System.MTProto.Port = *port
	}
	if *fakeSNI != "" {
		cfg.System.MTProto.FakeSNI = *fakeSNI
	}
	if *upstream != "" {
		cfg.System.MTProto.UpstreamMode = *upstream
	}

	switch {
	case len(secretFlags) > 0:
		cfg.System.MTProto.Secrets = nil
		for i, raw := range secretFlags {
			name, hex := splitNamedSecret(raw, fmt.Sprintf("secret-%d", i+1))
			sec, err := mtproto.ParseSecret(hex)
			if err != nil {
				log.Errorf("invalid -secret %q: %v", raw, err)
				os.Exit(1)
			}
			cfg.System.MTProto.Secrets = append(cfg.System.MTProto.Secrets, config.MTProtoSecret{
				ID: name, Name: name, Secret: sec.Hex(), Enabled: true,
			})
		}
	case len(hostFlags) > 0:
		cfg.System.MTProto.Secrets = nil
		for _, host := range hostFlags {
			sec, err := mtproto.GenerateSecret(host)
			if err != nil {
				log.Errorf("failed to generate secret for %q: %v", host, err)
				os.Exit(1)
			}
			cfg.System.MTProto.Secrets = append(cfg.System.MTProto.Secrets, config.MTProtoSecret{
				ID: host, Name: host, Secret: sec.Hex(), Enabled: true,
			})
		}
	}
	// If neither flag nor the config file provided any secrets, the server
	// auto-generates exactly one (against -fake-sni) the first time it
	// starts, and persists it back into cfg (and the config file, if any) -
	// see mtproto.buildSecrets / server.startLocked.
	cfg.System.MTProto.Enabled = true

	appCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Best-effort background refresher: current Telegram DC addresses and,
	// if enabled, the Cloudflare-Worker fallback domain list used when a
	// network blocks Telegram's WebSocket edge directly. Failures are
	// retried with backoff instead of waiting for the next periodic run.
	mtproto.StartUpstreamRefresh(appCtx, func() *config.Config { return &cfg })

	server := mtproto.NewServer(&cfg)
	if err := server.Start(); err != nil {
		log.Errorf("failed to start MTProto proxy: %v", err)
		os.Exit(1)
	}

	// server.Start() may have just auto-generated a secret and written it
	// back into cfg (same *Config pointer server.go holds); read the final,
	// effective list here so every secret in play gets its own link.
	printConnectionInfo(&cfg, server)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Infof("shutting down...")
	cancel()
	if err := server.Stop(); err != nil {
		log.Errorf("error while stopping: %v", err)
	}
}

// splitNamedSecret splits a "NAME=HEXSECRET" flag value into its parts,
// falling back to fallbackName when no "NAME=" prefix was given.
func splitNamedSecret(raw, fallbackName string) (name, hex string) {
	if i := strings.IndexByte(raw, '='); i > 0 {
		return raw[:i], raw[i+1:]
	}
	return fallbackName, raw
}

func printConnectionInfo(cfg *config.Config, s *mtproto.Server) {
	secrets := cfg.System.MTProto.EffectiveSecrets()
	if len(secrets) == 0 {
		return
	}

	host := publicHostHint(cfg.System.MTProto.BindAddress)
	port := cfg.System.MTProto.Port

	fmt.Println()
	fmt.Printf("MTProto proxy is running on port %d (fake SNI: %s).\n", port, cfg.System.MTProto.FakeSNI)
	if len(secrets) > 1 {
		fmt.Printf("%d secrets configured - each is an independent, individually revocable login:\n\n", len(secrets))
	}
	for _, sec := range secrets {
		printSecretLinks(sec.Name, sec.Secret, host, port, "  ")
	}
	fmt.Println()
	fmt.Println("  (replace \"" + host + "\" with your server's real public IP/hostname before sharing)")
	fmt.Println()

	stats := s.Stats()
	fmt.Printf("  connections: active=%d total=%d up=%d bytes down=%d bytes\n",
		stats.ActiveConnections, stats.TotalConnections, stats.BytesUp, stats.BytesDown)
	fmt.Println()
}

func printSecretLinks(name, secretHex, host string, port int, indent string) {
	link := fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", url.QueryEscape(host), port, secretHex)
	shareLink := fmt.Sprintf("https://t.me/proxy?server=%s&port=%d&secret=%s", url.QueryEscape(host), port, secretHex)
	fmt.Println(indent + name + ":")
	fmt.Println(indent + "  secret:      " + secretHex)
	fmt.Println(indent + "  tg link:     " + link)
	fmt.Println(indent + "  share link:  " + shareLink)
}

// publicHostHint returns something reasonable to put in the example
// connection link. It never tries to reach the network; if the proxy is
// bound to a specific address it uses that, otherwise it prints a
// placeholder for the user to fill in themselves.
func publicHostHint(bindAddr string) string {
	if bindAddr == "" || bindAddr == "0.0.0.0" || bindAddr == "::" {
		return "YOUR_SERVER_IP"
	}
	if ip := net.ParseIP(bindAddr); ip != nil {
		return bindAddr
	}
	return bindAddr
}

// ---------------------------------------------------------------------
// gen-secret: generate one or more fake-TLS secrets without starting a server
// ---------------------------------------------------------------------

func genSecretCmd(args []string) {
	fs := flag.NewFlagSet("gen-secret", flag.ExitOnError)
	var (
		host       = fs.String("host", "storage.googleapis.com", "fronting hostname to embed in the secret (this is what the fake-TLS handshake pretends to be)")
		name       = fs.String("name", "", "label for the generated secret (defaults to the hostname, or \"secret-N\" with -count > 1)")
		count      = fs.Int("count", 1, "how many secrets to generate")
		configPath = fs.String("config", "", "append the generated secret(s) to this JSON config file instead of just printing them (created if missing)")
		bind       = fs.String("bind", "", "bind address to use when printing example links (ignored with -config, which uses the file's own bind_address)")
		port       = fs.Int("port", 0, "port to use when printing example links (ignored with -config, which uses the file's own port)")
	)
	_ = fs.Parse(args)

	if *count < 1 {
		fmt.Fprintln(os.Stderr, "gen-secret: -count must be >= 1")
		os.Exit(1)
	}
	if *host == "" {
		fmt.Fprintln(os.Stderr, "gen-secret: -host must not be empty")
		os.Exit(1)
	}

	type generated struct {
		name, hex string
	}
	var out []generated
	for i := 0; i < *count; i++ {
		sec, err := mtproto.GenerateSecret(*host)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen-secret: %v\n", err)
			os.Exit(1)
		}
		label := *name
		switch {
		case label == "" && *count == 1:
			label = *host
		case label == "" && *count > 1:
			label = fmt.Sprintf("secret-%d", i+1)
		case label != "" && *count > 1:
			label = fmt.Sprintf("%s-%d", label, i+1)
		}
		out = append(out, generated{name: label, hex: sec.Hex()})
	}

	var linkHost string
	var linkPort int
	if *configPath != "" {
		cfg := config.DefaultConfig
		if err := cfg.LoadFromFile(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "gen-secret: failed to load %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		for _, g := range out {
			cfg.System.MTProto.Secrets = append(cfg.System.MTProto.Secrets, config.MTProtoSecret{
				ID: g.name, Name: g.name, Secret: g.hex, Enabled: true,
			})
		}
		if err := cfg.SaveToFile(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "gen-secret: failed to save %s: %v\n", *configPath, err)
			os.Exit(1)
		}
		linkHost = publicHostHint(cfg.System.MTProto.BindAddress)
		linkPort = cfg.System.MTProto.Port
		if linkPort == 0 {
			linkPort = 3128
		}
		fmt.Printf("Added %d secret(s) to %s. Restart mtproto-proxy to pick them up.\n\n", len(out), *configPath)
	} else {
		linkHost = publicHostHint(*bind)
		linkPort = *port
		if linkPort == 0 {
			linkPort = 3128
		}
	}

	for _, g := range out {
		printSecretLinks(g.name, g.hex, linkHost, linkPort, "")
		fmt.Println()
	}
	if *configPath == "" {
		fmt.Println("(these were only printed, not saved anywhere - pass -config to append them to a running proxy's config file, or -port/-bind to match your real setup)")
	}
}
