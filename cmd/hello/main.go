package main

import (
	"fmt"
	"log"
	"net/http"
)

func hello(w http.ResponseWriter, r *http.Request) {
	log.Printf("backend received: %s %s", r.Method, r.URL.Path)
    fmt.Fprintf(w, "method=%s path=%s\n", r.Method, r.URL.Path)
}

func main() {
	http.HandleFunc("/hello", hello)

	err := http.ListenAndServe("127.0.0.1:9001", nil)
	if err != nil {
		log.Fatal(err)
	}
}