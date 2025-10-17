package util

import (
	"net"
	"net/url"
	"sync"
)

type HostIps struct {
	URL string
	IPs []string
}

func BatchLookupURLsIP(hostUrls []string) map[string]HostIps {
	hostIps := make(map[string]HostIps)
	hostIpsLock := new(sync.Mutex)

	wg := new(sync.WaitGroup)
	wg.Add(len(hostUrls))
	for _, hostUrl := range hostUrls {
		go func(_hostUrl string) {
			defer wg.Done()
			u, err := url.Parse(_hostUrl)
			if err != nil {
				return
			}

			// 检查是否已存在需要加读锁
			hostIpsLock.Lock()
			_, exists := hostIps[u.Hostname()]
			hostIpsLock.Unlock()

			if exists {
				return
			}

			ip, err := net.LookupIP(u.Hostname())
			if err != nil {
				return
			}
			if len(ip) == 0 {
				return
			}
			var ips []string
			for _, ipAddr := range ip {
				ips = append(ips, ipAddr.String())
			}

			// 写入前再次检查（double-check），避免重复查询后重复写入
			hostIpsLock.Lock()
			defer hostIpsLock.Unlock()
			if _, exists := hostIps[u.Hostname()]; !exists {
				hostIps[u.Hostname()] = HostIps{
					URL: _hostUrl,
					IPs: ips,
				}
			}
		}(hostUrl)
	}
	wg.Wait()

	return hostIps
}

func UniqueIPs(hostIps map[string]HostIps) map[string][]string {
	hosts := make(map[string][]string)
	for host, hostIp := range hostIps {
		for _, ip := range hostIp.IPs {
			hosts[ip] = append(hosts[ip], host)
		}
	}
	return hosts
}
