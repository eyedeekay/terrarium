package main

import (
	"log"
	"os"
	"path/filepath"
	"syscall"
	
	"github.com/horgh/catbox"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetOutput(os.Stdout)

	args := catbox.GetArgs()
	if args == nil {
		os.Exit(1)
	}

	binPath, err := filepath.Abs(os.Args[0])
	if err != nil {
		log.Fatalf("Unable to determine absolute path to binary: %s: %s",
			os.Args[0], err)
	}

	cb, err := catbox.NewCatbox(args.ConfigFile)
	if err != nil {
		log.Fatal(err)
	}

	if err := cb.Start(args.ListenFD); err != nil {
		log.Fatal(err)
	}

	if cb.Restart {
		log.Printf("Shutdown completed. Restarting...")

		if err := syscall.Exec( // nolint: gas
			binPath,
			[]string{
				binPath,
				"-conf",
				cb.ConfigFile,
			},
			nil,
		); err != nil {
			log.Fatalf("Restart failed: %s", err)
		}

		log.Fatalf("not reached")
	}

	log.Printf("Server shutdown cleanly.")
}
