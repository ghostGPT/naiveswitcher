// naive-udp runs the optional UoT SOCKS endpoint against an existing Naive
// client, allowing deployment and testing without restarting TCP sessions.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"

	"naiveswitcher/pkg/proxy"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:1082", "SOCKS5 TCP/UDP listener")
	upstream := flag.String("upstream", "127.0.0.1:10790", "Existing Naive SOCKS5 listener")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("UDP-over-Naive listening on %s via %s", l.Addr(), *upstream)
	if err := proxy.ServeSOCKS(ctx, l, *upstream); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
