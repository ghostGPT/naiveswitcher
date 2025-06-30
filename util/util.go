package util

import (
	"net"
	"net/url"
	"sync"
)

func BatchLookupURLsIP(hostUrls []string) map[string][]string {
	hostIps := make(map[string][]string)
	ihLock := new(sync.Mutex)

	wg := new(sync.WaitGroup)
	wg.Add(len(hostUrls))
	for _, host := range hostUrls {
		go func(host string) {
			defer wg.Done()
			u, err := url.Parse(host)
			if err != nil {
				return
			}
			if _, ok := hostIps[u.Hostname()]; ok {
				return
			}
			ip, err := net.LookupIP(u.Hostname())
			if err != nil {
				return
			}
			if len(ip) == 0 {
				return
			}
			ihLock.Lock()
			defer ihLock.Unlock()
			for _, ipAddr := range ip {
				hostIps[u.Hostname()] = append(hostIps[u.Hostname()], ipAddr.String())
			}
		}(host)
	}
	wg.Wait()

	return hostIps
}
