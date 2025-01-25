package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"naiveswitcher/service"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	subscribeURL, listenPort, webPort string
	fastestUrl                        string
	hostUrls                          []string = []string{""}
	naiveCmd                          *exec.Cmd
	errorCount                        int

	version              string
	autoSwitchDuration   int
	dnsResolverIP        string  // Google DNS resolver.
	dnsResolverProto     = "udp" // Protocol to use for the DNS resolver
	dnsResolverTimeoutMs = 5000  // Timeout (ms) for the DNS resolver (optional)
)

func init() {
	dialer := &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: time.Duration(dnsResolverTimeoutMs) * time.Millisecond,
				}
				return d.DialContext(ctx, dnsResolverProto, dnsResolverIP)
			},
		},
	}
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}
	http.DefaultTransport.(*http.Transport).DialContext = dialContext
}

func main() {
	var showVersion bool

	flag.StringVar(&subscribeURL, "s", "https://example.com/sublink", "Subscribe to a URL")
	flag.StringVar(&listenPort, "l", "0.0.0.0:1080", "Listen port")
	flag.StringVar(&webPort, "w", "0.0.0.0:1081", "Web port")
	flag.StringVar(&dnsResolverIP, "r", "8.8.4.4:53", "DNS resolver IP")
	flag.BoolVar(&service.Debug, "d", false, "Debug mode")
	flag.IntVar(&autoSwitchDuration, "a", 30, "Auto switch fastest duration (minutes)")
	flag.StringVar(&hostUrls[0], "b", "", "Bootup node (default naive node https://a:b@domain:port)")
	flag.BoolVar(&showVersion, "v", false, "Show version")
	flag.Parse()

	if showVersion {
		println(version)
		return
	}

	service.Init()

	if subscribeURL == "" {
		println("Please provide a subscribe URL")
		return
	}

	if autoSwitchDuration < 30 {
		println("Auto switch duration must be at least 30 minutes")
		return
	}

	var err error
	hostUrls, err = handleSwitch(hostUrls)
	if err != nil {
		panic(err)
	}

	l, err := net.Listen("tcp", listenPort)
	if err != nil {
		panic(err)
	}

	service.DebugF("Running with naive: %s\n", service.Naive)

	doSwitch := make(chan struct{}, 1000)
	doCheckNaiveUpdate := make(chan struct{}, 10)

	go switcher(doSwitch)
	go updater(doCheckNaiveUpdate)

	doCheckNaiveUpdate <- struct{}{}

	go func() {
		ticker := time.NewTicker(time.Duration(autoSwitchDuration) * time.Minute)
		for range ticker.C {
			doSwitch <- struct{}{}
			doCheckNaiveUpdate <- struct{}{}
		}
	}()

	// graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go serveTCP(l, doSwitch)
	go serveWeb()

	<-ctx.Done()
	println("Shutting down")
	if naiveCmd != nil {
		if err := naiveCmd.Process.Kill(); err != nil {
			println("Error killing naive: ", err)
		}
		naiveCmd.Wait()
	}
	println("Shutdown complete")
}

func serveTCP(l net.Listener, doSwitch chan<- struct{}) {
	bufPool := &sync.Pool{
		New: func() interface{} {
			return make([]byte, 32*1024)
		},
	}
	for {
		conn, err := l.Accept()
		if err != nil {
			service.DebugF("Error accepting connection: %v\n", err)
			continue
		}
		go handleConnection(conn, bufPool, doSwitch)
	}
}

func serveWeb() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		service.WriteLog(w)
	})
	http.ListenAndServe(webPort, nil)
}

func switcher(doSwitch <-chan struct{}) {
	var switching bool
	var err error
	for range doSwitch {
		if switching {
			continue
		}
		switching = true
		service.DebugF("Error count exceeded, switching\n")
		go func() {
			defer func() {
				switching = false
			}()
			hostUrls, err = handleSwitch(hostUrls)
			if err != nil {
				service.DebugF("Error switching: %v\n", err)
			}
		}()
	}
}

func updater(signal <-chan struct{}) {
	var checking bool
	for range signal {
		if checking {
			return
		}
		checking = true
		go func() {
			defer func() {
				checking = false
			}()

			service.DebugF("Checking for naive update\n")
			ctx, cancel := context.WithTimeout(context.Background(), (time.Duration(autoSwitchDuration/2))*time.Minute)
			defer cancel()

			latestNaiveVersion, err := service.GitHubCheckGetLatestRelease(ctx, "klzgrad", "naiveproxy", service.Naive)
			if err != nil {
				service.DebugF("Error getting latest remote naive version: %v\n", err)
			}
			if latestNaiveVersion == nil {
				service.DebugF("No new version\n")
				return
			}

			newNaive, err := service.GitHubDownloadAsset(ctx, *latestNaiveVersion)
			if err != nil {
				service.DebugF("Error downloading asset: %v\n", err)
				return
			}

			if naiveCmd != nil {
				if err := naiveCmd.Process.Kill(); err != nil {
					service.DebugF("Error killing naive: %v\n", err)
				}
				naiveCmd.Wait()
				naiveCmd = nil
			}

			os.Remove(service.BasePath + "/" + service.Naive)
			service.Naive = newNaive

			naiveCmd, err = service.NaiveCmd(fastestUrl)
			if err != nil {
				service.DebugF("Error creating naive command: %v\n", err)
				return
			}

			if err := naiveCmd.Start(); err != nil {
				service.DebugF("Error starting naive: %v\n", err)
			}

			service.DebugF("Updated to %s\n", service.Naive)
		}()
	}
}

func handleSwitch(oldHostUrls []string) ([]string, error) {
	hostUrls, err := service.Subscription(subscribeURL)
	if err != nil {
		service.DebugF("Error updating subscription: %v\n", err)
		hostUrls = oldHostUrls
	}

	newFastestUrl, err := service.Fastest(hostUrls)
	if err != nil {
		service.DebugF("Error choosing fastest: %v\n", err)
		return nil, err
	}
	if fastestUrl == newFastestUrl {
		return hostUrls, errors.New("no change")
	}
	fastestUrl = newFastestUrl
	service.DebugF("Fastest: %s\n", fastestUrl)

	if naiveCmd != nil {
		if err := naiveCmd.Process.Kill(); err != nil {
			service.DebugF("Error killing naive: %v\n", err)
		}
		naiveCmd.Wait()
		naiveCmd = nil
	}

	naiveCmd, err = service.NaiveCmd(newFastestUrl)
	if err != nil {
		service.DebugF("Error creating naive command: %v\n", err)
		return nil, err
	}
	if err := naiveCmd.Start(); err != nil {
		service.DebugF("Error starting naive: %v\n", err)
		return nil, err
	}

	return hostUrls, nil
}

func handleConnection(conn net.Conn, bufPool *sync.Pool, doSwitch chan<- struct{}) {
	defer conn.Close()

	if naiveCmd == nil {
		service.DebugF("No naive running\n")
		doSwitch <- struct{}{}
		return
	}

	var serverDone bool = true
	var remoteOk bool

	naiveConn, err := net.Dial("tcp", service.UpstreamListenPort)
	if err == nil {
		go func() {
			defer naiveConn.Close()
			_, e := io.Copy(naiveConn, conn)
			remoteOk = e == nil
		}()
		buf := bufPool.Get()
		written, _ := io.CopyBuffer(service.NewDowngradeReaderWriter(conn), service.NewDowngradeReaderWriter(naiveConn), buf.([]byte))
		serverDone = isServerDone(int(written), buf.([]byte), remoteOk)
		bufPool.Put(buf)
	}

	if serverDone {
		// magic number for server connection error
		errorCount++
	} else {
		errorCount = 0
	}

	if errorCount > 10 {
		errorCount = 0
		doSwitch <- struct{}{}
	}
}

func isServerDone(written int, data []byte, remoteOk bool) bool {
	if remoteOk {
		return false
	}
	if written != 12 {
		return false
	}
	for i, v := range data[:12] {
		switch i {
		case 0:
			if v != 5 {
				return false
			}
		case 3:
			if v != 1 {
				return false
			}
		default:
			if v != 0 {
				return false
			}
		}
	}
	return true
}
