// Command mock-qconn runs the protocol-faithful qconn mock (internal/mockqnx)
// as a standalone server for local development and manual testing of qconn-mcp
// without QNX hardware.
package main

import (
	"flag"
	"log"

	"github.com/johnson-thomas/qnx8-qconn-mcp/internal/mockqnx"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8000", "listen address")
	flag.Parse()
	log.Printf("mock-qconn listening on %s", *addr)
	if err := mockqnx.ListenAndServe(*addr); err != nil {
		log.Fatalf("mock-qconn: %v", err)
	}
}
