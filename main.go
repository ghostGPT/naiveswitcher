package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	proping "github.com/prometheus-community/pro-bing"

	"naiveswitcher/service"
	"naiveswitcher/util"
)

var (
	version string

	subscribeURL, listenPort, webPort string
	autoSwitchDuration                int
	dnsResolverIP                     string  // Google DNS resolver.
	dnsResolverProto                  = "tcp" // Protocol to use for the DNS resolver
	dnsResolverTimeoutMs              = 5000  // Timeout (ms) for the DNS resolver (optional)

	errorCount         int
	naiveCmd           *exec.Cmd
	fastestUrl         string
	hostUrls           []string = []string{""}
	serverDownPriority          = make(map[string]int)
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
	flag.StringVar(&dnsResolverIP, "r", "1.0.0.1:53", "DNS resolver IP")
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
	hostUrls, err = handleSwitch(hostUrls, false)
	if err != nil {
		panic(err)
	}

	l, err := net.Listen("tcp", listenPort)
	if err != nil {
		panic(err)
	}

	service.DebugF("Running with naive: %s\n", service.Naive)

	doSwitch := make(chan bool, 1000)
	doCheckNaiveUpdate := make(chan struct{}, 10)

	go switcher(doSwitch)
	go updater(doCheckNaiveUpdate)

	doCheckNaiveUpdate <- struct{}{}

	go func() {
		ticker := time.NewTicker(time.Duration(autoSwitchDuration) * time.Minute)
		for range ticker.C {
			doSwitch <- false
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

func serveTCP(l net.Listener, doSwitch chan<- bool) {
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
	http.HandleFunc("/v", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fmt.Sprintf("Current: %s, ErrorCount: %d\n", fastestUrl, errorCount)))
		w.Write([]byte(fmt.Sprintf("DownStat: %+v\n", serverDownPriority)))
		w.Write([]byte("v" + version + "\n"))
	})
	http.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) {
		newHostUrls, err := service.Subscription(subscribeURL)
		if err != nil {
			w.Write([]byte(err.Error() + "\n"))
		} else {
			hostUrls = newHostUrls
		}
		w.Write([]byte(fmt.Sprintf("%d servers in pool\n", len(hostUrls))))
		wg := new(sync.WaitGroup)
		wg.Add(len(hostUrls))
		sb := new(strings.Builder)
		for _, host := range hostUrls {
			go func(host string) {
				defer wg.Done()
				u, err := url.Parse(host)
				if err != nil {
					w.Write([]byte(err.Error()))
					return
				}
				ip, err := net.LookupIP(u.Hostname())
				if err != nil {
					w.Write([]byte(err.Error()))
					return
				}
				sb.WriteString(ip[0].String() + "\n")
			}(host)
		}
		wg.Wait()
		w.Write([]byte(sb.String()))
	})
	http.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		sb := new(strings.Builder)
		wg := new(sync.WaitGroup)
		wg.Add(len(hostUrls))
		for _, host := range hostUrls {
			go func(host string) {
				defer wg.Done()
				u, err := url.Parse(host)
				if err != nil {
					w.Write([]byte(err.Error()))
					return
				}
				p, pingErr := proping.NewPinger(u.Hostname())
				p.Timeout = time.Second * 10
				if pingErr == nil {
					pingErr = p.Run()
				}
				sb.WriteString(fmt.Sprintf("%s, avg: %v, err: %v\n", u.Hostname(), p.Statistics().AvgRtt, pingErr))
			}(host)
		}
		wg.Wait()
		w.Write([]byte(sb.String()))
	})
	http.ListenAndServe(webPort, nil)
}

func switcher(doSwitch <-chan bool) {
	var switching bool
	var err error
	for isDown := range doSwitch {
		if switching {
			continue
		}
		switching = true
		errorCount = 0
		service.DebugF("Switching server...\n")
		go func() {
			defer func() {
				switching = false
				errorCount = 0
				service.DebugF("Switching done\n")
			}()
			hostUrls, err = handleSwitch(hostUrls, isDown)
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

func handleSwitch(oldHostUrls []string, isDown bool) ([]string, error) {
	if isDown {
		u, err := url.Parse(fastestUrl)
		if err != nil {
			return nil, err
		}
		serverDownPriority[u.Hostname()]++
	}

	hostUrls, err := service.Subscription(subscribeURL)
	if err != nil {
		service.DebugF("Error updating subscription: %v\n", err)
		hostUrls = oldHostUrls
	}

	newFastestUrl, err := service.Fastest(hostUrls, serverDownPriority)
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

func handleConnection(conn net.Conn, bufPool *sync.Pool, doSwitch chan<- bool) {
	defer conn.Close()

	if naiveCmd == nil {
		service.DebugF("No naive running\n")
		doSwitch <- false
		return
	}

	var serverDown bool = true
	var remoteOk bool

	naiveConn, err := net.Dial("tcp", service.UpstreamListenPort)
	if err == nil {
		go func() {
			defer naiveConn.Close()
			_, e := io.Copy(naiveConn, conn)
			remoteOk = e == nil
		}()
		buf := bufPool.Get()
		written, _ := io.CopyBuffer(util.NewDowngradeReaderWriter(conn), util.NewDowngradeReaderWriter(naiveConn), buf.([]byte))
		serverDown = isServerDown(int(written), buf.([]byte), remoteOk)
		bufPool.Put(buf)
	}

	if serverDown {
		errorCount++
	} else if errorCount > 0 {
		errorCount--
	}

	if errorCount > 10 {
		errorCount = 0
		service.DebugF("Too many errors, gonna switch\n")
		doSwitch <- true
	}
}

func isServerDown(written int, data []byte, remoteOk bool) bool {
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
