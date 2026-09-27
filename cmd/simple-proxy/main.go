package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
)

func main() {
	addresses := []string{
      "http://127.0.0.1:9001",
      "http://127.0.0.1:9002",
  	}

	var proxies []*httputil.ReverseProxy

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
      proxies = append(proxies, proxy)
  	}
	
	var mu sync.Mutex
	next := 0

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      mu.Lock()
      chosen := proxies[next]
      next = (next + 1) % len(proxies)
      mu.Unlock()

      chosen.ServeHTTP(w, r)
  })

	log.Println("proxy listening on 127.0.0.1:8080")
	err := http.ListenAndServe("127.0.0.1:8080", handler)
	if err != nil {
		log.Fatal(err)
	}
}