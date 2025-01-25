package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
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

func Fastest(hostUrls []string) (string, error) {
	type result struct {
		host string
		err  error
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results := make(chan result, len(hostUrls))
	closeLock := new(sync.Mutex)
	var closed bool

	for _, host := range hostUrls {
		go func(host string) {
			var finalError error

			defer func() {
				closeLock.Lock()
				if !closed {
					results <- result{host: host, err: finalError}
				}
				closeLock.Unlock()
			}()

			proxyUrl, err := url.Parse(host)
			if err != nil {
				finalError = err
				return
			}

			proxiedClient := &http.Client{Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
				},
				Proxy: http.ProxyURL(proxyUrl)},
			}

			req, err := http.NewRequest("GET", fmt.Sprintf("%s://%s/1Mb.dat", proxyUrl.Scheme, proxyUrl.Host), nil)
			if err != nil {
				finalError = err
				return
			}
			req = req.WithContext(ctx)

			resp, err := proxiedClient.Do(req)
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

			if len(body) < 200 {
				finalError = fmt.Errorf("invalid response, status code: %d, body: %s", resp.StatusCode, string(body))
				return
			}
		}(host)
	}

	var fastest string
	for range hostUrls {
		res := <-results
		if res.err != nil {
			DebugF("check activity failed, host: %s, error: %s\n", res.host, res.err)
			continue
		}
		if fastest == "" {
			fastest = res.host
		}
		break
	}

	closeLock.Lock()
	closed = true
	close(results)
	closeLock.Unlock()

	if fastest == "" {
		return "", fmt.Errorf("no valid hosts found")
	}

	return fastest, nil
}
