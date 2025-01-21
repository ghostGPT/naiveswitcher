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

	resp, err := http.Get(subscribeURL)
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

			req, err := http.NewRequest("GET", "http://www.google.com/generate_204", nil)
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

			_, err = io.ReadAll(resp.Body)
			if err != nil {
				finalError = err
				return
			}

			if resp.StatusCode != http.StatusNoContent {
				finalError = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
				return
			}
		}(host)
	}

	var fastest string
	for range hostUrls {
		res := <-results
		if res.err != nil {
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
