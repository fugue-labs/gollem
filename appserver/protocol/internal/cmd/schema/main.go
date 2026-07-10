package main

import (
	"fmt"
	"os"

	"github.com/fugue-labs/gollem/appserver/protocol"
)

func main() {
	data, err := protocol.MarshalJSONSchema()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile("schema.json", data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
