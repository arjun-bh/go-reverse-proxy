package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
)

func main() {
	port := flag.String("port", "9001", "port to listen on")
	id := flag.String("id", "A", "backend identity")
	flag.Parse()

	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("backend %s received: %s %s", *id, r.Method, r.URL.Path)
		fmt.Fprintf(w, "backend=%s method=%s path=%s\n",
			*id, r.Method, r.URL.Path)
	})

	err := http.ListenAndServe("127.0.0.1:"+*port, nil)
	if err != nil {
		log.Fatal(err)
	}
}
