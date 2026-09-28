package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

type Backend struct {
	Address string
	Proxy   *httputil.ReverseProxy
	Healthy bool
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

	var mu sync.Mutex
	next := 0

	//Round robin logic with locks
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		var chosen *Backend
		//Check to make sure Backend is healthy. If not, increment next and check next
		for checked := 0; checked < len(backends); checked++ {
			candidate := backends[next]
			next = (next + 1) % len(backends)
			if candidate.Healthy {
				chosen = candidate
				break
			}
		}
		mu.Unlock()

		if chosen == nil {
			http.Error(w, "no healthy backends", http.StatusServiceUnavailable)
			return
		}

		chosen.Proxy.ServeHTTP(w, r)
	})

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

				mu.Lock()
				backend.Healthy = healthy
				mu.Unlock()

				log.Printf("backend=%s healthy=%t", backend.Address, healthy)
			}

			<-ticker.C
		}
	}()

	log.Println("proxy listening on 127.0.0.1:8080")
	err := http.ListenAndServe("127.0.0.1:8080", handler)
	if err != nil {
		log.Fatal(err)
	}
}
