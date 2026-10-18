package subscription

import (
	"bufio"
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"naiveswitcher/pkg/log"
	"naiveswitcher/util"
)

func Subscription(subscribeURL string) ([]string, error) {
	const maxSubscriptionBytes = 16 << 20
	var hostUrls []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	subscribeURL = strings.Replace(subscribeURL, "http2://", "https://", 1)

	req, err := http.NewRequest("GET", subscribeURL, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	userInfo := resp.Header.Get("Subscription-Userinfo")
	log.DebugF("Userinfo: %s\n", userInfo)

	defer resp.Body.Close()
	limited := &io.LimitedReader{R: resp.Body, N: maxSubscriptionBytes + 1}
	scanner := bufio.NewScanner(base64.NewDecoder(base64.StdEncoding, limited))
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.Replace(line, "http2://", "https://", 1)
		u, err := url.Parse(line)
		if err != nil {
			return nil, err
		}
		hostDecoded, err := base64.StdEncoding.DecodeString(u.Host)
		if err != nil {
			return nil, err
		}
		hostUrls = append(hostUrls, "https://"+string(hostDecoded))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("subscription exceeds %d bytes", maxSubscriptionBytes)
	}

	return hostUrls, nil
}

func Fastest(hostUrls []string, serverPriority map[string]int, deadServer string) (string, error) {
	return fastest(hostUrls, serverPriority, deadServer, "")
}

// FastestWithCurrent requires a meaningful improvement before disrupting sessions.
func FastestWithCurrent(hostUrls []string, serverPriority map[string]int, current string) (string, error) {
	return fastest(hostUrls, serverPriority, "", current)
}

func worthSwitching(current, candidate time.Duration) bool {
	return current-candidate >= 50*time.Millisecond && candidate <= current*4/5
}

func fastest(hostUrls []string, serverPriority map[string]int, deadServer, current string) (string, error) {
	hostIps := util.BatchLookupURLsIP(hostUrls)
	ipHostMap := make(map[string][]util.HostIps)
	for _, ips := range hostIps {
		if len(ips.IPs) == 0 {
			continue
		}
		ipHostMap[ips.IPs[0]] = append(ipHostMap[ips.IPs[0]], ips)
	}

	if len(ipHostMap) == 0 {
		return "", fmt.Errorf("no hosts")
	}

	type result struct {
		host    *url.URL
		err     error
		elapsed time.Duration
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results := make(chan result, len(ipHostMap)+1)
	closeLock := new(sync.Mutex)
	var closed bool

	aliveHosts := new(sync.Map)

	var probes []string
	for _, hosts := range ipHostMap {
		var u string
		if len(hosts) == 1 {
			u = hosts[0].URL
		} else {
		RE_RAND:
			u = hosts[rand.Intn(len(hosts))].URL
			if u == deadServer {
				goto RE_RAND
			}
		}
		probes = append(probes, u)
	}
	// Always measure the current node, even if another hostname shares its IP.
	if current != "" && slices.Contains(hostUrls, current) && !slices.Contains(probes, current) {
		probes = append(probes, current)
	}
	for _, u := range probes {
		go func(host string) {
			started := time.Now()
			var finalError error
			var proxyUrl *url.URL

			defer func() {
				closeLock.Lock()
				if !closed {
					results <- result{host: proxyUrl, err: finalError, elapsed: time.Since(started)}
				}
				closeLock.Unlock()
			}()

			proxyUrl, finalError = url.Parse(host)
			if finalError != nil {
				return
			}

			aliveHosts.Store(proxyUrl.Hostname(), struct{}{})

			req, err := http.NewRequest("GET", fmt.Sprintf("%s://%s/1Mb.dat", proxyUrl.Scheme, proxyUrl.Host), nil)
			if err != nil {
				finalError = err
				return
			}
			req = req.WithContext(ctx)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				finalError = err
				return
			}
			defer resp.Body.Close()

			probe := make([]byte, 1024)
			n, err := io.ReadFull(resp.Body, probe)
			if err != nil {
				finalError = fmt.Errorf("invalid response, status code: %d, body: %s: %w", resp.StatusCode, string(probe[:n]), err)
				return
			}
		}(u)
	}

	latencies := make(map[string]time.Duration)
	var fastest []*url.URL
	var resultCount int
	for res := range results {
		resultCount++
		if res.err != nil {
			log.DebugF("check activity failed, host: %s, error: %s\n", res.host, res.err)
		} else {
			fastest = append(fastest, res.host)
			latencies[res.host.String()] = res.elapsed
		}
		if (current == "" && len(fastest) > 2) || resultCount >= len(probes) {
			break
		}
	}

	closeLock.Lock()
	closed = true
	close(results)
	closeLock.Unlock()

	if len(fastest) == 0 {
		return "", fmt.Errorf("no valid hosts found")
	}

	slices.SortFunc(fastest, func(a, b *url.URL) int {
		if priority := cmp.Compare(serverPriority[a.Hostname()], serverPriority[b.Hostname()]); priority != 0 {
			return priority
		}
		return cmp.Compare(latencies[a.String()], latencies[b.String()])
	})

	var minCount int
	for k, v := range serverPriority {
		if _, has := hostIps[k]; !has {
			delete(serverPriority, k)
		}
		if minCount == 0 || v < minCount {
			minCount = v
		}
	}
	if minCount > 100 {
		for k := range serverPriority {
			if serverPriority[k] <= minCount {
				delete(serverPriority, k)
				continue
			}
			serverPriority[k] -= minCount
		}
	}

	if currentLatency, healthy := latencies[current]; current != "" && healthy {
		candidateLatency := latencies[fastest[0].String()]
		if !worthSwitching(currentLatency, candidateLatency) {
			return current, nil
		}
	}
	if deadServer == "" || len(fastest) == 1 {
		return fastest[0].String(), nil
	}
	deadServerUrl, err := url.Parse(deadServer)
	if err != nil {
		return fastest[0].String(), nil
	}
	if deadServerUrl.Hostname() == fastest[0].Hostname() {
		return fastest[1].String(), nil
	}
	return fastest[0].String(), nil
}
