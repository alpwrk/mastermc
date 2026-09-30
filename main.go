// MasterMC – a lightweight web panel for hosting a Minecraft server.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"mastermc/internal/api"
	"mastermc/internal/auth"
	"mastermc/internal/config"
	"mastermc/internal/events"
)

//go:embed web
var webFiles embed.FS

var version = "dev"

func main() {
	port := flag.Int("port", 7777, "port of the web interface")
	bind := flag.String("bind", "0.0.0.0", "address the panel binds to")
	dataDir := flag.String("data", "./mastermc-data", "data directory (server, Java, configuration)")
	resetPw := flag.Bool("reset-password", false, "generate a new random password and exit")
	flag.Parse()

	dir, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		log.Fatalf("loading configuration: %v", err)
	}

	if cfg.Get().PasswordHash == "" || *resetPw {
		pw := auth.RandomPassword()
		hash, err := auth.HashPassword(pw)
		if err != nil {
			log.Fatal(err)
		}
		if err := cfg.Update(func(s *config.Settings) { s.PasswordHash = hash }); err != nil {
			log.Fatal(err)
		}
		pwFile := filepath.Join(dir, "initial-password.txt")
		os.WriteFile(pwFile, []byte(pw+"\n"), 0o600)
		fmt.Println("==================================================")
		fmt.Println("  MasterMC admin password:", pw)
		fmt.Println("  (saved to", pwFile+")")
		fmt.Println("  Please change it in the settings after your first login.")
		fmt.Println("==================================================")
		if *resetPw {
			return
		}
	}

	web, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	app := api.New(cfg, events.NewHub(), auth.NewManager(), web)

	addr := net.JoinHostPort(*bind, fmt.Sprint(*port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("port %d not available: %v", *port, err)
	}
	log.Printf("MasterMC %s running on http://%s (data: %s)", version, displayAddr(*bind, *port), dir)

	if cfg.Get().Autostart {
		go func() {
			if err := app.Server.Start(); err != nil {
				app.Server.Log("[panel] Autostart failed: " + err.Error())
			}
		}()
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down – stopping Minecraft server …")
	app.Server.StopAndWait(2 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func displayAddr(bind string, port int) string {
	if bind == "0.0.0.0" || bind == "::" || bind == "" {
		if ip := outboundIP(); ip != "" {
			return fmt.Sprintf("%s:%d", ip, port)
		}
		return fmt.Sprintf("localhost:%d", port)
	}
	return net.JoinHostPort(bind, fmt.Sprint(port))
}

// outboundIP determines the LAN IP (without actually sending packets).
func outboundIP() string {
	c, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}
