// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

// runServe serves a directory on loopback with a request log, for the local latest.json of the
// updater test.
func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	dir := flags.String("dir", ".", "directory to serve")
	addr := flags.String("addr", "127.0.0.1:0", "listen address")
	addrFile := flags.String("addr-file", "", "file to write the bound address to")
	flags.Parse(args)
	files := http.FileServer(http.Dir(*dir))
	server := &http.Server{Addr: *addr, ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s %s", r.Method, r.URL.Path, r.UserAgent())
		files.ServeHTTP(w, r)
	})}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	bound := listener.Addr().String()
	if *addrFile != "" {
		if err := os.WriteFile(*addrFile, []byte(bound), 0o644); err != nil {
			return err
		}
	}
	log.Printf("serving %s on %s", *dir, bound)
	return server.Serve(listener)
}
