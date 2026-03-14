// Package network provides concurrent IPv4 subnet scanning for ASIC miners.
package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/goasic/goasic/pkg/miners"
)

// ScanResult is the result of scanning a single IP.
type ScanResult struct {
	IP    string
	Miner miners.Miner
	Err   error
}

// Scanner scans a set of IP addresses concurrently.
type Scanner struct {
	hosts         []string
	maxConcurrent int
}

// NewScanner creates a scanner over an explicit list of hosts.
func NewScanner(hosts []string, maxConcurrent int) *Scanner {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if maxConcurrent > 512 {
		maxConcurrent = 512
	}
	return &Scanner{hosts: hosts, maxConcurrent: maxConcurrent}
}

// NewSubnetScanner parses a CIDR subnet and creates a scanner for all host IPs.
// Example: NewSubnetScanner("192.168.1.0/24", 100)
func NewSubnetScanner(cidr string, maxConcurrent int) (*Scanner, error) {
	hosts, err := cidrHosts(cidr)
	if err != nil {
		return nil, err
	}
	log.Printf("scanner: %d hosts in subnet %s", len(hosts), cidr)
	return NewScanner(hosts, maxConcurrent), nil
}

// Scan probes all hosts concurrently and streams results via the returned channel.
// The channel is closed when all probes complete.
func (s *Scanner) Scan(ctx context.Context) <-chan ScanResult {
	results := make(chan ScanResult, len(s.hosts))
	sem := make(chan struct{}, s.maxConcurrent)
	var wg sync.WaitGroup

	for _, ip := range s.hosts {
		ip := ip
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			miner, err := miners.Detect(ctx, ip)
			if err == nil && miner != nil {
				results <- ScanResult{IP: ip, Miner: miner}
			} else if err != nil && !isNotFound(err) {
				results <- ScanResult{IP: ip, Err: err}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	return results
}

// ScanAll is a convenience wrapper that collects all found miners.
func ScanAll(ctx context.Context, cidr string, maxConcurrent int) ([]miners.Miner, error) {
	scanner, err := NewSubnetScanner(cidr, maxConcurrent)
	if err != nil {
		return nil, err
	}
	var found []miners.Miner
	for result := range scanner.Scan(ctx) {
		if result.Miner != nil {
			found = append(found, result.Miner)
			log.Printf("scanner: found miner at %s (%s)", result.IP, result.Miner.Brand())
		}
	}
	log.Printf("scanner: complete — %d miner(s) found", len(found))
	return found, nil
}

// cidrHosts returns all host (non-network, non-broadcast) IPs in a CIDR block.
func cidrHosts(cidr string) ([]string, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("network: invalid CIDR %q: %w", cidr, err)
	}
	var hosts []string
	ip := network.IP.Mask(network.Mask)
	for {
		next := nextIP(ip)
		if !network.Contains(next) {
			break
		}
		// Skip network address and broadcast
		if !next.Equal(network.IP) {
			hosts = append(hosts, next.String())
		}
		ip = next
	}
	return hosts, nil
}

func nextIP(ip net.IP) net.IP {
	ip = ip.To4()
	if ip == nil {
		return nil
	}
	n := binary.BigEndian.Uint32(ip)
	n++
	next := make(net.IP, 4)
	binary.BigEndian.PutUint32(next, n)
	return next
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() != "" && len(err.Error()) > 0 &&
		(containsAny(err.Error(), "no miner detected", "connection refused", "i/o timeout", "no route"))
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
