package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
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

	"github.com/blang/semver"
	proping "github.com/prometheus-community/pro-bing"
	"github.com/rhysd/go-github-selfupdate/selfupdate"

	"naiveswitcher/service"
	"naiveswitcher/util"
)

const (
	dnsResolverProto     = "udp" // Protocol to use for the DNS resolver
	dnsResolverTimeoutMs = 5000  // Timeout (ms) for the DNS resolver (optional)
)

var (
	version string

	subscribeURL, listenPort, webPort string
	autoSwitchDuration                int
	dnsResolverIP                     string // Google DNS resolver.
	bootstrapNode                     string

	errorCount         int
	naiveCmd           *exec.Cmd
	fastestUrl         string
	hostUrls           []string
	serverDownPriority = make(map[string]int)
	gracefulShutdown   context.CancelFunc
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
	flag.StringVar(&bootstrapNode, "b", "", "Bootup node (default naive node https://a:b@domain:port)")
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
	if len(hostUrls) == 0 {
		hostUrls = append(hostUrls, bootstrapNode)
	}
	hostUrls, err = handleSwitch(hostUrls, "")
	if err != nil {
		println("Bootstrap error:", err.Error())
	}

	l, err := net.Listen("tcp", listenPort)
	if err != nil {
		panic(err)
	}

	service.DebugF("Running with naive: %s\n", service.Naive)

	doSwitch := make(chan string, 100)
	doCheckUpdate := make(chan struct{}, 10)

	go switcher(doSwitch)
	go updater(doCheckUpdate)

	doCheckUpdate <- struct{}{}

	go func() {
		ticker := time.NewTicker(time.Duration(autoSwitchDuration) * time.Minute)
		for range ticker.C {
			doSwitch <- ""
			doCheckUpdate <- struct{}{}
		}
	}()

	// graceful shutdown
	var ctxWithCalcel context.Context
	ctxWithCalcel, gracefulShutdown = context.WithCancel(context.Background())
	ctx, stop := signal.NotifyContext(ctxWithCalcel, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go serveTCP(l, doSwitch)
	go serveWeb(doSwitch, doCheckUpdate)

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

func serveTCP(l net.Listener, doSwitch chan<- string) {
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

func serveWeb(doSwitch chan<- string, doCheckUpdate chan<- struct{}) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		service.WriteLog(w)
	})
	http.HandleFunc("/v", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "text/html")
		changeNode := r.URL.Query().Get("changeNode")
		if changeNode == fastestUrl {
			doSwitch <- changeNode
			w.Write([]byte(`Switching...<br>will redirect in 3 seconds<script>setTimeout(function(){window.location.href="/v"},3000)</script>`))
			return
		}
		if r.URL.Query().Get("checkUpdate") == "true" {
			doCheckUpdate <- struct{}{}
			w.Write([]byte("Checking for updates...<br>will redirect in 3 seconds<script>setTimeout(function(){window.location.href=\"/v\"},3000)</script>"))
			return
		}
		changeNodeHref := fmt.Sprintf(`<a href="/v?changeNode=%s">[Change]</a>`, url.QueryEscape(fastestUrl))
		w.Write([]byte(fmt.Sprintf("Current: %s %s<br>", fastestUrl, changeNodeHref)))
		w.Write([]byte(fmt.Sprintf("ErrorCount: %d<br>", errorCount)))
		w.Write([]byte(fmt.Sprintf("DownStat: %+v<br>", serverDownPriority)))
		w.Write([]byte("<br>"))
		w.Write([]byte("naive v" + service.Naive + "<br>"))
		w.Write([]byte("naiveswitcher v" + version + "<br>"))
		w.Write([]byte("<a href=\"/v?checkUpdate=true\">[Check Update]</a><br>"))
	})
	http.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) {
		newHostUrls, err := service.Subscription(subscribeURL)
		if err != nil {
			w.Write([]byte(err.Error() + "\n"))
		} else {
			hostUrls = newHostUrls
		}
		w.Write([]byte(fmt.Sprintf("%d servers in pool\n", len(hostUrls))))
		hostIps := util.BatchLookupURLsIP(hostUrls)

		for host, ips := range hostIps {
			w.Write([]byte(fmt.Sprintf("%s: %+v\n", host, ips.IPs)))
		}

		w.Write([]byte("\n\n\n"))

		uniqueIPs := util.UniqueIPs(hostIps)
		for ip := range uniqueIPs {
			w.Write([]byte(fmt.Sprintf("%s\n", ip)))
		}
	})
	http.HandleFunc("/p", func(w http.ResponseWriter, r *http.Request) {
		hostIps := util.BatchLookupURLsIP(hostUrls)
		uniqueIps := util.UniqueIPs(hostIps)
		uniqueHosts := make(map[string]struct{})
		for _, hosts := range uniqueIps {
			uniqueHosts[hosts[rand.Intn(len(hosts))]] = struct{}{}
		}
		sb := new(strings.Builder)
		wg := new(sync.WaitGroup)
		wg.Add(len(uniqueHosts))
		for host := range uniqueHosts {
			go func(host string) {
				defer wg.Done()
				p, pingErr := proping.NewPinger(host)
				p.Timeout = time.Second * 10
				if pingErr == nil {
					pingErr = p.Run()
				}
				sb.WriteString(fmt.Sprintf("%s, avg: %v, err: %v\n", host, p.Statistics().AvgRtt, pingErr))
			}(host)
		}
		wg.Wait()
		w.Write([]byte(sb.String()))
	})
	http.ListenAndServe(webPort, nil)
}

func switcher(doSwitch <-chan string) {
	var switching bool
	var err error
	for isDown := range doSwitch {
		if switching {
			continue
		}
		switching = true
		errorCount = 0
		service.DebugF("isDown: %v, Switching server...\n", isDown)
		go func() {
			defer func() {
				switching = false
				errorCount = 0
				service.DebugF("Switching done\n")
			}()
			if len(hostUrls) == 0 {
				hostUrls = append(hostUrls, bootstrapNode)
			}
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

		go func() {
			v := semver.MustParse(version)
			latest, err := selfupdate.UpdateSelf(v, "ghostGPT/naiveswitcher")
			if err != nil {
				service.DebugF("Binary update failed: %v", err)
				return
			}
			if latest.Version.Equals(v) {
				service.DebugF("Current binary is the latest version", version)
			} else {
				service.DebugF("Successfully updated to version", latest.Version)
				service.DebugF("Release note:\n", latest.ReleaseNotes)
				gracefulShutdown()
			}
		}()
	}
}

func handleSwitch(oldHostUrls []string, downServer string) ([]string, error) {
	u, err := url.Parse(downServer)
	if err != nil {
		return nil, err
	}
	serverDownPriority[u.Hostname()]++

	hostUrls, err := service.Subscription(subscribeURL)
	if err != nil {
		service.DebugF("Error updating subscription: %v\n", err)
		hostUrls = oldHostUrls
	}

	newFastestUrl, err := service.Fastest(hostUrls, serverDownPriority, downServer)
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

func handleConnection(conn net.Conn, bufPool *sync.Pool, doSwitch chan<- string) {
	defer conn.Close()

	if naiveCmd == nil {
		service.DebugF("No naive running\n")
		doSwitch <- ""
		return
	}

	var serverDown bool = true
	var remoteOk bool

	naiveConn, err := net.DialTimeout("tcp", service.UpstreamListenPort, 3*time.Second)
	if err == nil {
		if tcpConn, ok := naiveConn.(*net.TCPConn); ok {
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(2 * time.Minute)
		}
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
		doSwitch <- fastestUrl
	}
}

var DataServerDown = map[[12]byte]struct{}{
	{
		5, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0,
	}: {},
}

func isServerDown(written int, data []byte, remoteOk bool) bool {
	if remoteOk {
		return false
	}
	if written != 12 {
		return false
	}
	var key [12]byte
	copy(key[:], data[:12])
	_, isDown := DataServerDown[key]
	return isDown
}
