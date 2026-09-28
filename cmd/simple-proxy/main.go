package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

// Struct for one backend server
type Backend struct {
	Address string
	Proxy   *httputil.ReverseProxy
	Healthy bool
}

// Server Pool struct for the collection of backend servers
type Pool struct {
	mu       sync.Mutex
	backends []*Backend
	next     int
}

// Health check function
func checkHealth(client *http.Client, address string) bool {
	response, err := client.Get(address + "/healthz")
	if err != nil {
		return false
	}
	defer response.Body.Close()

	return response.StatusCode == http.StatusOK
}

// Method for selecting target backend (round robin + healthy)
func (p *Pool) NextHealthy() *Backend {
	p.mu.Lock()
	var chosen *Backend
	//Check to make sure Backend is healthy. If not, increment next and check next
	for checked := 0; checked < len(p.backends); checked++ {
		candidate := p.backends[p.next]
		p.next = (p.next + 1) % len(p.backends)
		if candidate.Healthy {
			chosen = candidate
			break
		}
	}
	p.mu.Unlock()
	return chosen
}

// ServeHTTP forwards to a healthy backend or returns 503
func (p *Pool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	chosen := p.NextHealthy()

	if chosen == nil {
		http.Error(w, "no healthy backends", http.StatusServiceUnavailable)
		return
	}

	chosen.Proxy.ServeHTTP(w, r)
}

func main() {
	addresses := []string{
		"http://127.0.0.1:9001",
		"http://127.0.0.1:9002",
	}

	var backends []*Backend

	//Setup a reverse proxy for each backend server
	for _, address := range addresses {
		target, err := url.Parse(address)
		if err != nil {
			log.Fatal(err)
		}

		proxy := &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetURL(target)
			},
		}
		backends = append(backends, &Backend{
			Address: address,
			Proxy:   proxy,
			Healthy: false,
		})
	}

	pool := &Pool{
		backends: backends,
	}

	//Define health check client
	healthClient := &http.Client{
		Timeout: 100 * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	//Goroutine for checking backend server health periodically
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()

		for {
			//Go through each backend server and update the health status
			for _, backend := range backends {
				healthy := checkHealth(healthClient, backend.Address)

				pool.mu.Lock()
				backend.Healthy = healthy
				pool.mu.Unlock()

				log.Printf("backend=%s healthy=%t", backend.Address, healthy)
			}

			<-ticker.C
		}
	}()

	log.Println("proxy listening on 127.0.0.1:8080")
	err := http.ListenAndServe("127.0.0.1:8080", pool)
	if err != nil {
		log.Fatal(err)
	}
}
