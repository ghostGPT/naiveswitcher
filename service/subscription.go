package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"naiveswitcher/util"
)

func Subscription(subscribeURL string) ([]string, error) {
	var hostUrls []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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
	DebugF("Userinfo: %s\n", userInfo)

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	bodyDecoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewBuffer(bodyDecoded))
	for scanner.Scan() {
		line := scanner.Text()
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

	return hostUrls, nil
}

func Fastest(hostUrls []string, serverPriority map[string]int, isDown bool) (string, error) {
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
		host *url.URL
		err  error
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results := make(chan result, len(ipHostMap))
	closeLock := new(sync.Mutex)
	var closed bool

	aliveHosts := new(sync.Map)

	for _, hosts := range ipHostMap {
		var host string
		if len(hosts) == 1 {
			host = hosts[0].URL
		} else {
			host = hosts[rand.Intn(len(hosts))].URL
		}
		go func(host string) {
			var finalError error
			var proxyUrl *url.URL

			defer func() {
				closeLock.Lock()
				if !closed {
					results <- result{host: proxyUrl, err: finalError}
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

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				finalError = err
				return
			}

			if len(body) < 1024 {
				finalError = fmt.Errorf("invalid response, status code: %d, body: %s", resp.StatusCode, string(body))
				return
			}
		}(host)
	}

	var fastest []*url.URL
	var resultCount int
	for res := range results {
		resultCount++
		if res.err != nil {
			DebugF("check activity failed, host: %s, error: %s\n", res.host, res.err)
		} else {
			fastest = append(fastest, res.host)
		}
		if len(fastest) > 2 || resultCount >= len(ipHostMap) {
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
		return serverPriority[a.Hostname()] - serverPriority[b.Hostname()]
	})

	if !isDown {
		return fastest[0].String(), nil
	}

	// decrease all server priority by the minimum count
	var minCount int
	for k, v := range serverPriority {
		if _, has := aliveHosts.Load(k); !has {
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

	return fastest[0].String(), nil
}
