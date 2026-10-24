package main

import (
	"log"
	"os"

	"github.com/contenox/runtime/libsandbox"
	"github.com/contenox/runtime/runtime/contenoxcli"
)

func main() {
	if handled, err := libsandbox.ShimMain(); handled {
		if err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}
	contenoxcli.Main()
}
