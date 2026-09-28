package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"io"
	"net/http/httputil"
	"net/url"
	"strings"
)

func TestNextHealthyRoundRobin(t *testing.T) {
	a := &Backend{Address: "A", Healthy: true}
	b := &Backend{Address: "B", Healthy: true}

	pool := &Pool{
		backends: []*Backend{a, b},
	}

	expected := []*Backend{a, b, a, b}

	for i, want := range expected {
		got := pool.NextHealthy()
		if got != want {
			t.Fatalf("selection %d: got %p, want %p", i+1, got, want)
		}
	}
}

func TestNextHealthySkipsUnhealthy(t *testing.T) {
	a := &Backend{Address: "A", Healthy: true}
	b := &Backend{Address: "B", Healthy: false}
	c := &Backend{Address: "C", Healthy: true}

	pool := &Pool{
		backends: []*Backend{a, b, c},
	}

	expected := []*Backend{a, c, a, c}

	for i, want := range expected {
		got := pool.NextHealthy()
		if got != want {
			t.Fatalf("selection %d: got %p, want %p", i+1, got, want)
		}
	}
}

func TestNextHealthyReturnsNil(t *testing.T) {
	t.Run("empty pool", func(t *testing.T) {
		pool := &Pool{}

		if got := pool.NextHealthy(); got != nil {
			t.Fatalf("got %p, want nil", got)
		}
	})

	t.Run("all unhealthy", func(t *testing.T) {
		pool := &Pool{
			backends: []*Backend{
				{Address: "A", Healthy: false},
				{Address: "B", Healthy: false},
			},
		}

		if got := pool.NextHealthy(); got != nil {
			t.Fatalf("got %p, want nil", got)
		}
	})
}

func TestNextHealthyRecovery(t *testing.T) {
	a := &Backend{Address: "A", Healthy: true}
	b := &Backend{Address: "B", Healthy: false}

	pool := &Pool{
		backends: []*Backend{a, b},
	}

	if got := pool.NextHealthy(); got != a {
		t.Fatalf("before recovery: got %p, want A (%p)", got, a)
	}

	// Simulate the health checker recording a successful probe.
	pool.mu.Lock()
	b.Healthy = true
	pool.mu.Unlock()

	if got := pool.NextHealthy(); got != b {
		t.Fatalf("after recovery: got %p, want B (%p)", got, b)
	}

	if got := pool.NextHealthy(); got != a {
		t.Fatalf("after wraparound: got %p, want A (%p)", got, a)
	}
}

func TestNextHealthyConcurrent(t *testing.T) {
	a := &Backend{Address: "A", Healthy: true}
	b := &Backend{Address: "B", Healthy: true}
	pool := &Pool{backends: []*Backend{a, b}}

	const requests = 100
	results := make([]*Backend, requests)

	var wg sync.WaitGroup

	for i := 0; i < requests; i++ {
		wg.Go(func() {
			results[i] = pool.NextHealthy()
		})
	}

	wg.Wait()

	counts := make(map[*Backend]int)
	for _, backend := range results {
		counts[backend]++
	}

	if counts[a] != 50 || counts[b] != 50 {
		t.Fatalf("got A=%d B=%d; want 50 each", counts[a], counts[b])
	}
}

func TestNextHealthyConcurrentHealthUpdates(t *testing.T) {
	a := &Backend{Address: "A", Healthy: true}
	b := &Backend{Address: "B", Healthy: true}
	pool := &Pool{backends: []*Backend{a, b}}

	const selections = 1000
	results := make([]*Backend, selections)

	var wg sync.WaitGroup
	start := make(chan struct{})

	wg.Go(func() {
		<-start

		for i := 0; i < selections; i++ {
			pool.mu.Lock()
			b.Healthy = !b.Healthy
			pool.mu.Unlock()
		}
	})

	for worker := 0; worker < 10; worker++ {
		wg.Go(func() {
			<-start

			for i := worker; i < selections; i += 10 {
				results[i] = pool.NextHealthy()
			}
		})
	}

	close(start)
	wg.Wait()

	for i, got := range results {
		if got != a && got != b {
			t.Fatalf("selection %d: got %p, want A or B", i, got)
		}
	}
}

func TestCheckHealthStatus(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   bool
	}{
		{"healthy", http.StatusOK, true},
		{"server error", http.StatusInternalServerError, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/healthz" {
						t.Errorf("got path %q, want /healthz", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
						return
					}

					w.WriteHeader(tc.status)
				},
			))
			defer server.Close()

			got := checkHealth(server.Client(), server.URL)
			if got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
}

func TestCheckHealthTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		},
	))
	defer server.Close()

	client := server.Client()
	client.Timeout = 50 * time.Millisecond

	if got := checkHealth(client, server.URL); got {
		t.Fatal("got healthy=true, want false for a timed-out probe")
	}
}

func TestCheckHealthConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	))

	client := server.Client()
	client.Timeout = 100 * time.Millisecond
	address := server.URL

	// Stop the server before making the health-check request.
	server.Close()

	if got := checkHealth(client, address); got {
		t.Fatal("got healthy=true, want false for an unavailable backend")
	}
}

func TestServeHTTPNoHealthyBackends(t *testing.T) {
	pool := &Pool{
		backends: []*Backend{
			{Address: "A", Healthy: false},
			{Address: "B", Healthy: false},
		},
	}

	request := httptest.NewRequest(http.MethodGet, "/hello", nil)
	recorder := httptest.NewRecorder()

	pool.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("got status %d, want 503", recorder.Code)
	}

	if recorder.Body.String() != "no healthy backends\n" {
		t.Fatalf("unexpected body: %q", recorder.Body.String())
	}
}

func TestServeHTTPForwardsRequest(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("got method %q, want POST", r.Method)
			}
			if r.URL.Path != "/hello" {
				t.Errorf("got path %q, want /hello", r.URL.Path)
			}
			if r.URL.RawQuery != "name=Arjun" {
				t.Errorf("got query %q, want name=Arjun", r.URL.RawQuery)
			}

			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading request body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if string(body) != "request payload" {
				t.Errorf("got request body %q, want request payload", body)
			}

			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "response from backend")
		},
	))
	defer backend.Close()

	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
		},
	}

	pool := &Pool{
		backends: []*Backend{
			{
				Address: backend.URL,
				Proxy:   proxy,
				Healthy: true,
			},
		},
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/hello?name=Arjun",
		strings.NewReader("request payload"),
	)
	recorder := httptest.NewRecorder()

	pool.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201", recorder.Code)
	}
	if recorder.Body.String() != "response from backend" {
		t.Fatalf("unexpected response body: %q", recorder.Body.String())
	}
}

func TestServeHTTPUnavailableBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	))

	target, err := url.Parse(backend.URL)
	if err != nil {
		backend.Close()
		t.Fatal(err)
	}

	// Simulate a backend dying after its last successful health check.
	backend.Close()

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
		},
	}

	pool := &Pool{
		backends: []*Backend{
			{
				Address: backend.URL,
				Proxy:   proxy,
				Healthy: true,
			},
		},
	}

	request := httptest.NewRequest(http.MethodGet, "/hello", nil)
	recorder := httptest.NewRecorder()

	pool.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("got status %d, want 502", recorder.Code)
	}
}